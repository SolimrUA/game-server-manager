package main

import (
	"archive/zip"
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
	Config Config
}

func NewAssetPublisherStack(scope constructs.Construct, id string, props *AssetPublisherStackProps) awscdk.Stack {
	var stackProps awscdk.StackProps
	var config Config
	if props != nil {
		stackProps = props.StackProps
		config = props.Config
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

	awscdk.NewCfnOutput(stack, jsii.String("GeneratedTemplatePath"), &awscdk.CfnOutputProps{
		Value: jsii.String(config.OutputTemplatePath),
	})

	return stack
}

func main() {
	config, err := LoadConfig()
	must(err)
	must(prepareAssets(config))
	must(NewTemplateGenerator(config).Write())

	app := awscdk.NewApp(nil)
	NewAssetPublisherStack(app, "GameServerAssetPublisher", &AssetPublisherStackProps{
		StackProps: awscdk.StackProps{
			Env: env(config),
		},
		Config: config,
	})
	app.Synth(nil)
}

func env(config Config) *awscdk.Environment {
	return &awscdk.Environment{
		Account: jsii.String(config.AwsAccount),
		Region:  jsii.String(config.AwsRegion),
	}
}

func prepareAssets(config Config) error {
	if err := os.RemoveAll(config.LocalAssetBuildDir); err != nil {
		return err
	}

	if err := copyFile("../Bash/valheim.sh", filepath.Join(config.LocalBashBuildDir, "valheim.sh")); err != nil {
		return err
	}

	if err := copyDir("../FrontEnd", config.LocalFrontendBuildDir); err != nil {
		return err
	}

	lambdas := map[string]string{
		"../Lambda/gaming_server_start_stop-v1_0.py": filepath.Join(config.LocalLambdaBuildDir, "gaming_server_start_stop-v1_0.zip"),
		"../Lambda/mcUpdateDNS-v1_0.py":              filepath.Join(config.LocalLambdaBuildDir, "mcUpdateDNS-v1_0.zip"),
	}

	for source, destination := range lambdas {
		if err := zipLambda(source, destination); err != nil {
			return err
		}
	}

	return nil
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

func zipLambda(source, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}

	output, err := os.Create(destination)
	if err != nil {
		return err
	}
	defer output.Close()

	archive := zip.NewWriter(output)
	defer archive.Close()

	writer, err := archive.Create("lambda_function.py")
	if err != nil {
		return err
	}

	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()

	_, err = io.Copy(writer, input)
	return err
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
