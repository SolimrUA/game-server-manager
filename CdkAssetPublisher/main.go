package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awscloudfront"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsiam"
	"github.com/aws/aws-cdk-go/awscdk/v2/awss3"
	"github.com/aws/aws-cdk-go/awscdk/v2/awss3deployment"
	"github.com/aws/constructs-go/constructs/v10"
	"github.com/aws/jsii-runtime-go"
)

type AssetPublisherStackProps struct {
	awscdk.StackProps
	Config       Config
	ScriptHashes ScriptHashes
	ControlPanel *ControlPanelDeployment
}

// ControlPanelDeployment is set once the Control Panel stack exists to publish into. Until then there is
// no bucket to deploy to and the control panel is left out of this stack entirely.
type ControlPanelDeployment struct {
	BucketArn       string
	DistributionId  string
	ControlPanelUrl string
	SourceDir       string
}

// ScriptHashes holds full SHA256 hex digests of the game scripts that get fetched-and-executed on an
// EC2 instance. Server stacks take these as parameters and verify a script matches before ever running
// it, so a compromised bucket can't get anything executed that wasn't approved at deploy time.
type ScriptHashes struct {
	ValheimInstallSha256          string
	ValheimStatusAgentSha256      string
	VintageStoryInstallSha256     string
	VintageStoryStatusAgentSha256 string
}

func NewAssetPublisherStack(scope constructs.Construct, id string, props *AssetPublisherStackProps) awscdk.Stack {
	var stackProps awscdk.StackProps
	var config Config
	var scriptHashes ScriptHashes
	var controlPanel *ControlPanelDeployment
	if props != nil {
		stackProps = props.StackProps
		config = props.Config
		scriptHashes = props.ScriptHashes
		controlPanel = props.ControlPanel
	}

	stack := awscdk.NewStack(scope, &id, &stackProps)

	var bucket awss3.IBucket
	if config.CreateAssetBucket {
		bucket = awss3.NewBucket(stack, jsii.String("AssetBucket"), &awss3.BucketProps{
			BucketName: jsii.String(config.AssetBucketName),
			BlockPublicAccess: awss3.NewBlockPublicAccess(&awss3.BlockPublicAccessOptions{
				BlockPublicAcls:       jsii.Bool(true),
				IgnorePublicAcls:      jsii.Bool(true),
				BlockPublicPolicy:     jsii.Bool(false),
				RestrictPublicBuckets: jsii.Bool(false),
			}),
			Encryption: awss3.BucketEncryption_S3_MANAGED,
			EnforceSSL: jsii.Bool(true),
			Versioned:  jsii.Bool(true),
		})
	} else {
		bucket = awss3.Bucket_FromBucketName(stack, jsii.String("AssetBucket"), jsii.String(config.AssetBucketName))
	}

	bucket.AddToResourcePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Actions:    jsii.Strings("s3:GetObject"),
		Principals: &[]awsiam.IPrincipal{awsiam.NewAnyPrincipal()},
		Resources:  jsii.Strings(fmt.Sprintf("arn:aws:s3:::%s/%s/*", config.AssetBucketName, strings.Trim(config.AssetKeyPrefix, "/"))),
	}))

	// BucketDeployment uploads the prepared local files to the configured S3 bucket during cdk deploy.
	awss3deployment.NewBucketDeployment(stack, jsii.String("PublishDeploymentAssets"), &awss3deployment.BucketDeploymentProps{
		Sources:              &[]awss3deployment.ISource{awss3deployment.Source_Asset(jsii.String(config.LocalAssetBuildDir), nil)},
		DestinationBucket:    bucket,
		DestinationKeyPrefix: jsii.String(config.AssetKeyPrefix),
		Prune:                jsii.Bool(false),
	})

	// Straight to the bucket CloudFront serves. Prune keeps it to exactly what FrontEnd/ holds, and
	// RetainOnDelete leaves the files alone when this deployment goes away - the Control Panel stack owns
	// that bucket's lifecycle, not this one.
	if controlPanel != nil {
		awss3deployment.NewBucketDeployment(stack, jsii.String("PublishControlPanel"), &awss3deployment.BucketDeploymentProps{
			Sources: &[]awss3deployment.ISource{awss3deployment.Source_Asset(jsii.String(controlPanel.SourceDir), nil)},
			DestinationBucket: awss3.Bucket_FromBucketArn(stack, jsii.String("WebBucket"),
				jsii.String(controlPanel.BucketArn)),
			Prune:          jsii.Bool(true),
			RetainOnDelete: jsii.Bool(true),
			Distribution: awscloudfront.Distribution_FromDistributionAttributes(stack, jsii.String("WebDistribution"),
				&awscloudfront.DistributionAttributes{
					DistributionId: jsii.String(controlPanel.DistributionId),
					DomainName:     jsii.String(strings.TrimPrefix(controlPanel.ControlPanelUrl, "https://")),
				}),
			DistributionPaths: jsii.Strings("/*"),
		})
	}

	// The Fleet stack reads these at synth time to resolve each game template's script URL and digest.
	awscdk.NewCfnOutput(stack, jsii.String("AssetBucketName"), &awscdk.CfnOutputProps{
		Value: jsii.String(config.AssetBucketName),
	})
	awscdk.NewCfnOutput(stack, jsii.String("AssetKeyPrefix"), &awscdk.CfnOutputProps{
		Value: jsii.String(config.AssetKeyPrefix),
	})
	awscdk.NewCfnOutput(stack, jsii.String("ScriptsBaseUrl"), &awscdk.CfnOutputProps{
		Value: jsii.String(fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s/scripts", config.AssetBucketName, config.AwsRegion, strings.Trim(config.AssetKeyPrefix, "/"))),
	})
	awscdk.NewCfnOutput(stack, jsii.String("ValheimInstallSha256"), &awscdk.CfnOutputProps{
		Value: jsii.String(scriptHashes.ValheimInstallSha256),
	})
	awscdk.NewCfnOutput(stack, jsii.String("ValheimStatusAgentSha256"), &awscdk.CfnOutputProps{
		Value: jsii.String(scriptHashes.ValheimStatusAgentSha256),
	})
	awscdk.NewCfnOutput(stack, jsii.String("VintageStoryInstallSha256"), &awscdk.CfnOutputProps{
		Value: jsii.String(scriptHashes.VintageStoryInstallSha256),
	})
	awscdk.NewCfnOutput(stack, jsii.String("VintageStoryStatusAgentSha256"), &awscdk.CfnOutputProps{
		Value: jsii.String(scriptHashes.VintageStoryStatusAgentSha256),
	})

	return stack
}

func main() {
	config, err := LoadConfig()
	must(err)
	scriptHashes, err := prepareAssets(config)
	must(err)

	controlPanel, err := prepareControlPanel(config)
	must(err)

	app := awscdk.NewApp(nil)
	NewAssetPublisherStack(app, "GameServerAssetPublisher", &AssetPublisherStackProps{
		StackProps: awscdk.StackProps{
			Env: env(config),
		},
		Config:       config,
		ScriptHashes: scriptHashes,
		ControlPanel: controlPanel,
	})
	app.Synth(nil)
}

// prepareControlPanel stages FrontEnd/ with config.js filled in from the Control Panel stack's outputs.
// Returns nil - and says so - when that stack isn't readable yet, so a first install can publish the
// scripts and Lambda code it needs before the Control Panel exists.
func prepareControlPanel(config Config) (*ControlPanelDeployment, error) {
	outputs, found, err := lookupControlPanelOutputs(config.ControlPanelStackName, config.AwsRegion)
	if err != nil {
		return nil, err
	}
	if !found {
		fmt.Printf("NOT PUBLISHING THE CONTROL PANEL: couldn't read outputs from stack %q, so the "+
			"site stays exactly as it is. Deploy that stack first, or set CONTROL_PANEL_STACK_NAME "+
			"to the one that exists, then run this again.\n", config.ControlPanelStackName)
		return nil, nil
	}

	if err := stageControlPanel(config.LocalControlPanelBuildDir, config.ControlPanelStackName, outputs); err != nil {
		return nil, err
	}

	fmt.Printf("Staged the control panel for %s from %s\n", outputs.ControlPanelBucketArn, config.LocalControlPanelBuildDir)
	return &ControlPanelDeployment{
		BucketArn:       outputs.ControlPanelBucketArn,
		DistributionId:  outputs.DistributionId,
		ControlPanelUrl: outputs.ControlPanelUrl,
		SourceDir:       config.LocalControlPanelBuildDir,
	}, nil
}

func env(config Config) *awscdk.Environment {
	return &awscdk.Environment{
		Account: jsii.String(config.AwsAccount),
		Region:  jsii.String(config.AwsRegion),
	}
}

func prepareAssets(config Config) (ScriptHashes, error) {
	if err := os.RemoveAll(config.LocalAssetBuildDir); err != nil {
		return ScriptHashes{}, err
	}

	if err := copyDir("../scripts", config.LocalScriptsBuildDir); err != nil {
		return ScriptHashes{}, err
	}

	valheimInstallHash, err := sha256File("../scripts/Valheim/install.sh")
	if err != nil {
		return ScriptHashes{}, err
	}
	valheimStatusAgentHash, err := sha256File("../scripts/Valheim/status_agent.py")
	if err != nil {
		return ScriptHashes{}, err
	}
	vsInstallHash, err := sha256File("../scripts/VintageStory/install.sh")
	if err != nil {
		return ScriptHashes{}, err
	}
	vsStatusAgentHash, err := sha256File("../scripts/VintageStory/status_agent.py")
	if err != nil {
		return ScriptHashes{}, err
	}

	return ScriptHashes{
		ValheimInstallSha256:          valheimInstallHash,
		ValheimStatusAgentSha256:      valheimStatusAgentHash,
		VintageStoryInstallSha256:     vsInstallHash,
		VintageStoryStatusAgentSha256: vsStatusAgentHash,
	}, nil
}

// sha256File returns the full lowercase-hex SHA256 digest of a file's contents, in the same format
// `sha256sum` produces - so it can be compared with `sha256sum -c` on the receiving end without any
// reformatting.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func copyDir(source, destination string) error {
	var files []string
	if err := filepath.WalkDir(source, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		files = append(files, path)
		return nil
	}); err != nil {
		return err
	}

	sort.Strings(files)
	for _, sourcePath := range files {
		relativePath, err := filepath.Rel(source, sourcePath)
		if err != nil {
			return err
		}
		if err := copyFile(sourcePath, filepath.Join(destination, relativePath)); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(source, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}

	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()

	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer output.Close()

	_, err = io.Copy(output, input)
	return err
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
