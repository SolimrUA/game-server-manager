package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsiam"
	"github.com/aws/aws-cdk-go/awscdk/v2/awss3"
	"github.com/aws/aws-cdk-go/awscdk/v2/awss3deployment"
	"github.com/aws/constructs-go/constructs/v10"
	"github.com/aws/jsii-runtime-go"
)

type AssetsStackProps struct {
	awscdk.StackProps
	Config Config
	// DigestsDir holds the per-game lists of script digests, published alongside the scripts
	// themselves.
	DigestsDir string
}

// NewAssetsStack publishes the game scripts an instance downloads as it installs itself, and the
// lists of digests it checks them against. All of it is readable by anyone, because an instance
// fetches it before it has credentials of its own, and nothing is run before it matches a digest
// that traces back to the one hash the instance was given.
func NewAssetsStack(scope constructs.Construct, id string, props *AssetsStackProps) awscdk.Stack {
	stack := awscdk.NewStack(scope, &id, &props.StackProps)
	config := props.Config

	publicRead := awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Actions:    jsii.Strings("s3:GetObject"),
		Principals: &[]awsiam.IPrincipal{awsiam.NewAnyPrincipal()},
		Resources:  jsii.Strings(fmt.Sprintf("arn:aws:s3:::%s/%s/*", config.AssetBucketName, assetPrefix(config))),
	})

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
		// Merges into the policy the bucket already has, which EnforceSSL put a statement in.
		bucket.AddToResourcePolicy(publicRead)
	} else {
		bucket = awss3.Bucket_FromBucketName(stack, jsii.String("AssetBucket"), jsii.String(config.AssetBucketName))
		// AddToResourcePolicy does nothing on an imported bucket, so the whole policy is written
		// here - EnforceSSL's statement included, because a bucket has one and this replaces it.
		awss3.NewBucketPolicy(stack, jsii.String("AssetBucketPolicy"), &awss3.BucketPolicyProps{
			Bucket:        bucket,
			RemovalPolicy: awscdk.RemovalPolicy_RETAIN,
		}).Document().AddStatements(denyInsecureTransport(config), publicRead)
	}

	// Prune is off: the prefix is shared with whatever else is published under it.
	awss3deployment.NewBucketDeployment(stack, jsii.String("PublishScripts"), &awss3deployment.BucketDeploymentProps{
		Sources: &[]awss3deployment.ISource{
			awss3deployment.Source_Asset(jsii.String("../scripts"), nil),
			awss3deployment.Source_Asset(jsii.String(props.DigestsDir), nil),
		},
		DestinationBucket:    bucket,
		DestinationKeyPrefix: jsii.String(assetPrefix(config) + "/scripts"),
		Prune:                jsii.Bool(false),
	})

	return stack
}

// denyInsecureTransport is what EnforceSSL puts on a bucket this stack creates, repeated for one it
// imports so both end up refusing plain HTTP.
func denyInsecureTransport(config Config) awsiam.PolicyStatement {
	arn := fmt.Sprintf("arn:aws:s3:::%s", config.AssetBucketName)
	return awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Effect:     awsiam.Effect_DENY,
		Actions:    jsii.Strings("s3:*"),
		Principals: &[]awsiam.IPrincipal{awsiam.NewAnyPrincipal()},
		Resources:  jsii.Strings(arn, arn+"/*"),
		Conditions: &map[string]interface{}{"Bool": map[string]interface{}{"aws:SecureTransport": "false"}},
	})
}

func assetPrefix(config Config) string {
	return strings.Trim(config.AssetKeyPrefix, "/")
}

// scriptsBaseUrl is where an instance fetches its install script from, which is a property of the
// bucket's name and prefix rather than of anything deployed.
func scriptsBaseUrl(config Config) string {
	return fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s/scripts",
		config.AssetBucketName, config.AwsRegion, assetPrefix(config))
}

// scriptDigest is the digest an instance checks a downloaded script against, in the format
// `sha256sum -c` reads.
func scriptDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func textDigest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
