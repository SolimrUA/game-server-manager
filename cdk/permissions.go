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
		Actions:    jsii.Strings("ec2:StartInstances", "ec2:StopInstances", "ec2:ModifyInstanceAttribute", "ec2:DeleteTags"),
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

	// Vault and role names follow a fixed convention per game, so they are derived from the game name.
	// A tag lookup isn't an option for the role: the Resource Groups Tagging API doesn't index IAM roles.
	add(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("backup:ListRecoveryPointsByBackupVault", "backup:StartBackupJob"),
		Resources: jsii.Strings(fmt.Sprintf("arn:aws:backup:%s:%s:backup-vault:game-server-*-backup-vault-daily", region, account)),
	})
	add(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("iam:PassRole"),
		Resources: jsii.Strings(fmt.Sprintf("arn:aws:iam::%s:role/service-role/game-server-*-cfn-backup-role", account)),
		Conditions: &map[string]interface{}{
			"StringEquals": map[string]string{"iam:PassedToService": "backup.amazonaws.com"},
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

	// Pausing and resuming a server's idle-shutdown schedule.
	add(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("events:DescribeRule", "events:EnableRule", "events:DisableRule"),
		Resources: jsii.Strings(fmt.Sprintf("arn:aws:events:%s:%s:rule/game-server-*-cfn-idle-check", region, account)),
	})

	// Listing users for the access page, and writing back a display name.
	add(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("cognito-idp:ListUsers", "cognito-idp:ListUsersInGroup", "cognito-idp:AdminUpdateUserAttributes"),
		Resources: &[]*string{userPool.UserPoolArn()},
	})
}
