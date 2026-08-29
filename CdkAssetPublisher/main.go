package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsiam"
	"github.com/aws/aws-cdk-go/awscdk/v2/awss3"
	"github.com/aws/aws-cdk-go/awscdk/v2/awss3deployment"
	"github.com/aws/constructs-go/constructs/v10"
	"github.com/aws/jsii-runtime-go"
)

type AssetPublisherStackProps struct {
	awscdk.StackProps
	Config       Config
	LambdaKeys   LambdaKeys
	ScriptHashes ScriptHashes
}

// LambdaKeys holds the content-addressed S3 keys (relative to AssetKeyPrefix) that each Lambda zip was
// published under - see zipLambdaContentAddressed for why these need to change whenever the code does.
type LambdaKeys struct {
	StartStopLambdaKey string
	UpdateDnsLambdaKey string
}

// ScriptHashes holds full SHA256 hex digests of the game scripts that get fetched-and-executed on an
// EC2 instance. Server stacks take these as parameters and verify a script matches before ever running
// it, so a compromised bucket can't get anything executed that wasn't approved at deploy time.
type ScriptHashes struct {
	ValheimSha256                 string
	VintageStoryInstallSha256     string
	VintageStoryStatusAgentSha256 string
}

func NewAssetPublisherStack(scope constructs.Construct, id string, props *AssetPublisherStackProps) awscdk.Stack {
	var stackProps awscdk.StackProps
	var config Config
	var lambdaKeys LambdaKeys
	var scriptHashes ScriptHashes
	if props != nil {
		stackProps = props.StackProps
		config = props.Config
		lambdaKeys = props.LambdaKeys
		scriptHashes = props.ScriptHashes
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

	// These are the values to pass as --parameter-overrides to `aws cloudformation deploy` for
	// cfn/control-panel.yaml (AssetsBucketName/AssetsKeyPrefix/StartStopLambdaKey/UpdateDnsLambdaKey) and
	// cfn/server-stack.yaml (GameServer, composed as ScriptsBaseUrl + "/<script>", e.g.
	// ScriptsBaseUrl + "/valheim.sh"). See the root README.
	awscdk.NewCfnOutput(stack, jsii.String("AssetBucketName"), &awscdk.CfnOutputProps{
		Value: jsii.String(config.AssetBucketName),
	})
	awscdk.NewCfnOutput(stack, jsii.String("AssetKeyPrefix"), &awscdk.CfnOutputProps{
		Value: jsii.String(config.AssetKeyPrefix),
	})
	awscdk.NewCfnOutput(stack, jsii.String("ScriptsBaseUrl"), &awscdk.CfnOutputProps{
		Value: jsii.String(fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s/scripts", config.AssetBucketName, config.AwsRegion, strings.Trim(config.AssetKeyPrefix, "/"))),
	})
	awscdk.NewCfnOutput(stack, jsii.String("StartStopLambdaKey"), &awscdk.CfnOutputProps{
		Value: jsii.String(lambdaKeys.StartStopLambdaKey),
	})
	awscdk.NewCfnOutput(stack, jsii.String("UpdateDnsLambdaKey"), &awscdk.CfnOutputProps{
		Value: jsii.String(lambdaKeys.UpdateDnsLambdaKey),
	})
	awscdk.NewCfnOutput(stack, jsii.String("ValheimSha256"), &awscdk.CfnOutputProps{
		Value: jsii.String(scriptHashes.ValheimSha256),
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
	lambdaKeys, scriptHashes, err := prepareAssets(config)
	must(err)

	app := awscdk.NewApp(nil)
	NewAssetPublisherStack(app, "GameServerAssetPublisher", &AssetPublisherStackProps{
		StackProps: awscdk.StackProps{
			Env: env(config),
		},
		Config:       config,
		LambdaKeys:   lambdaKeys,
		ScriptHashes: scriptHashes,
	})
	app.Synth(nil)
}

func env(config Config) *awscdk.Environment {
	return &awscdk.Environment{
		Account: jsii.String(config.AwsAccount),
		Region:  jsii.String(config.AwsRegion),
	}
}

func prepareAssets(config Config) (LambdaKeys, ScriptHashes, error) {
	if err := os.RemoveAll(config.LocalAssetBuildDir); err != nil {
		return LambdaKeys{}, ScriptHashes{}, err
	}

	if err := copyDir("../scripts", config.LocalScriptsBuildDir); err != nil {
		return LambdaKeys{}, ScriptHashes{}, err
	}

	if err := copyDir("../FrontEnd", config.LocalFrontendBuildDir); err != nil {
		return LambdaKeys{}, ScriptHashes{}, err
	}

	startStopKey, err := zipLambdaContentAddressed(config, "../Lambda/gaming_server_start_stop-v1_0.py", "gaming_server_start_stop-v1_0")
	if err != nil {
		return LambdaKeys{}, ScriptHashes{}, err
	}
	updateDnsKey, err := zipLambdaContentAddressed(config, "../Lambda/update-dns-v1_0.py", "update-dns-v1_0")
	if err != nil {
		return LambdaKeys{}, ScriptHashes{}, err
	}

	valheimHash, err := sha256File("../scripts/valheim.sh")
	if err != nil {
		return LambdaKeys{}, ScriptHashes{}, err
	}
	vsInstallHash, err := sha256File("../scripts/VintageStory/install.sh")
	if err != nil {
		return LambdaKeys{}, ScriptHashes{}, err
	}
	vsStatusAgentHash, err := sha256File("../scripts/VintageStory/status_agent.py")
	if err != nil {
		return LambdaKeys{}, ScriptHashes{}, err
	}

	return LambdaKeys{StartStopLambdaKey: startStopKey, UpdateDnsLambdaKey: updateDnsKey},
		ScriptHashes{
			ValheimSha256:                 valheimHash,
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

// zipLambdaContentAddressed zips source into config.LocalLambdaBuildDir under a name that includes a hash
// of its content, and returns the resulting key relative to AssetKeyPrefix (e.g. "Lambda/foo.deadbeef.zip").
//
// This matters because AWS::Lambda::Function only re-fetches code from S3 when the Code.S3Key VALUE itself
// changes in the CloudFormation template - it has no way to know the object AT an unchanged key was
// overwritten with different bytes, so a fixed filename would let code fixes silently fail to deploy.
func zipLambdaContentAddressed(config Config, source, baseName string) (string, error) {
	var buf bytes.Buffer
	archive := zip.NewWriter(&buf)

	writer, err := archive.Create("lambda_function.py")
	if err != nil {
		return "", err
	}

	input, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer input.Close()

	if _, err := io.Copy(writer, input); err != nil {
		return "", err
	}
	if err := archive.Close(); err != nil {
		return "", err
	}

	hash := sha256.Sum256(buf.Bytes())
	fileName := fmt.Sprintf("%s.%x.zip", baseName, hash[:6])
	destination := filepath.Join(config.LocalLambdaBuildDir, fileName)

	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(destination, buf.Bytes(), 0644); err != nil {
		return "", err
	}

	return "Lambda/" + fileName, nil
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
