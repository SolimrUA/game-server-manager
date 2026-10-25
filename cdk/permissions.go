package main

import (
	"fmt"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awscognito"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsiam"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/jsii-runtime-go"
)

// addControlLambdaPermissions grants the control Lambda what it needs to act on game servers. Anything
// that can change or stop a server is conditioned on the tag pair identifying a managed instance, so an
// instance outside this solution is out of reach whatever the caller asks for.
func addControlLambdaPermissions(stack awscdk.Stack, fn awslambda.Function, userPool awscognito.IUserPool, statusBucketName *string, config Config) {
	region := *stack.Region()
	account := *stack.Account()

	managedInstanceOnly := &map[string]interface{}{
		"StringEquals": map[string]string{
			fmt.Sprintf("ec2:ResourceTag/%s", config.IdTagName): config.IdTagValue,
		},
	}

	add := func(props *awsiam.PolicyStatementProps) {
		fn.AddToRolePolicy(awsiam.NewPolicyStatement(props))
	}

	// Describe* has no resource-level scoping at all - scoped to an ARN these are silently ineffective.
	add(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("ec2:DescribeInstances", "ec2:DescribeTags", "ec2:DescribeInstanceTypes", "ec2:DescribeVolumes"),
		Resources: jsii.Strings("*"),
	})
	add(&awsiam.PolicyStatementProps{
		Actions:    jsii.Strings("ec2:StartInstances", "ec2:StopInstances", "ec2:ModifyInstanceAttribute", "ec2:CreateTags", "ec2:DeleteTags"),
		Resources:  jsii.Strings(fmt.Sprintf("arn:aws:ec2:%s:%s:instance/*", region, account)),
		Conditions: managedInstanceOnly,
	})

	// The status snapshot each server pushes, and the world zips a download produces.
	add(&awsiam.PolicyStatementProps{
		Actions: jsii.Strings("s3:GetObject"),
		Resources: &[]*string{
			jsii.String(fmt.Sprintf("arn:aws:s3:::%s/status/*", *statusBucketName)),
			jsii.String(fmt.Sprintf("arn:aws:s3:::%s/worlds/*", *statusBucketName)),
		},
	})

	// World downloads, mod management and game restarts all reach the instance through Run Command.
	add(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("ssm:SendCommand"),
		Resources: jsii.Strings(fmt.Sprintf("arn:aws:ec2:%s:%s:instance/*", region, account)),
		Conditions: &map[string]interface{}{
			"StringEquals": map[string]string{
				fmt.Sprintf("ssm:resourceTag/%s", config.IdTagName): config.IdTagValue,
			},
		},
	})
	add(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("ssm:SendCommand"),
		Resources: jsii.Strings(fmt.Sprintf("arn:aws:ssm:%s::document/AWS-RunShellScript", region)),
	})
	add(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("ssm:GetCommandInvocation"),
		Resources: jsii.Strings("*"),
	})

	// Listing users for the access page, and writing back a display name.
	add(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("cognito-idp:ListUsers", "cognito-idp:ListUsersInGroup", "cognito-idp:AdminUpdateUserAttributes"),
		Resources: &[]*string{userPool.UserPoolArn()},
	})
}

// addServerCreationPermissions lets the control Lambda create and remove game servers. Every grant that
// acts on an existing resource is conditioned on the managed tag, and RunInstances is conditioned on the
// Lambda applying that tag, so the function can only ever produce and act on servers of its own making.
func addServerCreationPermissions(stack awscdk.Stack, fn awslambda.Function, fleet Fleet, config Config, statusBucketName *string) {
	region := *stack.Region()
	account := *stack.Account()

	managedTag := fmt.Sprintf("aws:RequestTag/%s", config.ManagedTag)
	resourceTag := fmt.Sprintf("ec2:ResourceTag/%s", config.ManagedTag)

	add := func(props *awsiam.PolicyStatementProps) {
		fn.AddToRolePolicy(awsiam.NewPolicyStatement(props))
	}

	// The instance type is restricted to the sizes the resize action already offers, so a caller cannot
	// ask for an arbitrarily large one.
	add(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("ec2:RunInstances"),
		Resources: jsii.Strings(fmt.Sprintf("arn:aws:ec2:%s:%s:instance/*", region, account)),
		Conditions: &map[string]interface{}{
			"StringEquals": map[string]interface{}{
				managedTag:         config.ManagedValue,
				"ec2:InstanceType": []string{"t3a.micro", "t3a.small", "t3a.medium", "t3a.large"},
			},
		},
	})
	// RunInstances also touches the image, subnet, group, profile and the volume it creates, none of
	// which carry the request's tags.
	add(&awsiam.PolicyStatementProps{
		Actions: jsii.Strings("ec2:RunInstances"),
		Resources: &[]*string{
			jsii.String(fmt.Sprintf("arn:aws:ec2:%s::image/*", region)),
			jsii.String(fmt.Sprintf("arn:aws:ec2:%s:%s:subnet/*", region, account)),
			jsii.String(fmt.Sprintf("arn:aws:ec2:%s:%s:security-group/*", region, account)),
			jsii.String(fmt.Sprintf("arn:aws:ec2:%s:%s:network-interface/*", region, account)),
			jsii.String(fmt.Sprintf("arn:aws:ec2:%s:%s:volume/*", region, account)),
			//A server started from a backup has its volume made from that snapshot as it launches.
			//Snapshot ARNs carry no account id.
			jsii.String(fmt.Sprintf("arn:aws:ec2:%s::snapshot/*", region)),
		},
	})
	add(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("ec2:CreateTags"),
		Resources: jsii.Strings(fmt.Sprintf("arn:aws:ec2:%s:%s:*/*", region, account)),
		Conditions: &map[string]interface{}{
			"StringEquals": map[string]string{"ec2:CreateAction": "RunInstances"},
		},
	})

	add(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("ec2:CreateVolume"),
		Resources: jsii.Strings(fmt.Sprintf("arn:aws:ec2:%s:%s:volume/*", region, account)),
		Conditions: &map[string]interface{}{
			"StringEquals": map[string]string{managedTag: config.ManagedValue},
		},
	})
	// The data volume is tagged after the launch that created it, because tagging volumes during the
	// launch would tag the root disk too, and the backup Lambda selects data volumes on that tag.
	add(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("ec2:CreateTags"),
		Resources: jsii.Strings(fmt.Sprintf("arn:aws:ec2:%s:%s:volume/*", region, account)),
		Conditions: &map[string]interface{}{
			"StringEquals": map[string]string{managedTag: config.ManagedValue},
		},
	})

	// Terminating is limited to servers already carrying the managed tag.
	add(&awsiam.PolicyStatementProps{
		Actions: jsii.Strings("ec2:TerminateInstances"),
		Resources: &[]*string{
			jsii.String(fmt.Sprintf("arn:aws:ec2:%s:%s:instance/*", region, account)),
			jsii.String(fmt.Sprintf("arn:aws:ec2:%s:%s:volume/*", region, account)),
		},
		Conditions: &map[string]interface{}{
			"StringEquals": map[string]string{resourceTag: config.ManagedValue},
		},
	})

	add(&awsiam.PolicyStatementProps{
		Actions: jsii.Strings("ec2:DescribeImages", "ec2:DescribeSubnets", "ec2:DescribeSecurityGroups",
			"ec2:DescribeSnapshots"),
		Resources: jsii.Strings("*"),
	})

	// The installer reports its outcome through a presigned PUT, which only works if the signer itself
	// may write the object.
	add(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("s3:PutObject"),
		Resources: jsii.Strings(fmt.Sprintf("arn:aws:s3:::%s/status/*", *statusBucketName)),
	})
	// The AMI is read at the moment a server is created, so a new Ubuntu release needs no redeploy.
	add(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("ssm:GetParameter"),
		Resources: jsii.Strings(fmt.Sprintf("arn:aws:ssm:%s::parameter%s", region, config.AmiParameter)),
	})
	// A new server's join password is written here before it boots, and read back to carry the password
	// of the server a backup was taken from.
	add(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("ssm:PutParameter", "ssm:GetParameter"),
		Resources: jsii.Strings(fmt.Sprintf("arn:aws:ssm:%s:%s:parameter/game-password-*", region, account)),
	})

	add(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("iam:PassRole"),
		Resources: jsii.Strings(fmt.Sprintf("arn:aws:iam::%s:role/*", account)),
		Conditions: &map[string]interface{}{
			"StringEquals": map[string]string{"iam:PassedToService": "ec2.amazonaws.com"},
		},
	})
	//Reading when a server was last backed up. Taking one is the backup Lambda's job.
	add(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("backup:ListRecoveryPointsByBackupVault"),
		Resources: &[]*string{fleet.BackupVaultArn},
	})
}
