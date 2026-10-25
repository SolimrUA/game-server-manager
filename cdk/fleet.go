package main

import (
	"fmt"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsbackup"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsec2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsevents"
	"github.com/aws/aws-cdk-go/awscdk/v2/awseventstargets"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsiam"
	"github.com/aws/aws-cdk-go/awscdk/v2/awskms"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/constructs-go/constructs/v10"
	"github.com/aws/jsii-runtime-go"
)

type FleetStackProps struct {
	awscdk.StackProps
	Config Config
}

// Fleet is what the control panel needs in order to create a server against this stack.
type Fleet struct {
	Stack              awscdk.Stack
	InstanceProfileArn *string
	SecurityGroupIds   map[string]*string
	BackupVaultName    *string
	BackupVaultArn     *string
	BackupLambda       awslambda.IFunction
}

// NewFleetStack holds what every game server shares: the role they run under, a security group per
// game, the vault its backups are kept in, and the schedules that back servers up and stop idle ones.
//
// Backups and idle shutdown both select servers by the managed tag, so each covers every server the
// control panel created and nothing else.
func NewFleetStack(scope constructs.Construct, id string, props *FleetStackProps) Fleet {
	stack := awscdk.NewStack(scope, &id, &props.StackProps)
	config := props.Config

	statusBucketName := awscdk.Fn_ImportValue(jsii.String("common-infra-status-bucket-name"))
	vpc := awsec2.Vpc_FromVpcAttributes(stack, jsii.String("Vpc"), &awsec2.VpcAttributes{
		VpcId:             awscdk.Fn_ImportValue(jsii.String("common-infra-vpc-id")),
		AvailabilityZones: jsii.Strings(*awscdk.Fn_ImportValue(jsii.String("common-infra-subnet-az"))),
	})

	profile := addServerRole(stack, statusBucketName)
	groups := addSecurityGroups(stack, vpc)
	vault, backupRole := addBackups(stack, config)
	backupLambda := addServerBackups(stack, config, statusBucketName, vault, backupRole)
	addIdleShutdown(stack, config, statusBucketName)

	return Fleet{
		Stack:              stack,
		InstanceProfileArn: profile.InstanceProfileArn(),
		SecurityGroupIds:   groups,
		BackupVaultName:    vault.BackupVaultName(),
		BackupVaultArn:     vault.BackupVaultArn(),
		BackupLambda:       backupLambda,
	}
}

// addServerRole creates the role every game server runs under. Its grants cover the status and
// world-download prefixes, and the join-password parameters, for every server.
func addServerRole(stack awscdk.Stack, statusBucketName *string) awsiam.InstanceProfile {
	role := awsiam.NewRole(stack, jsii.String("GameServerRole"), &awsiam.RoleProps{
		AssumedBy:   awsiam.NewServicePrincipal(jsii.String("ec2.amazonaws.com"), nil),
		Description: jsii.String("Role every game server instance runs under"),
		ManagedPolicies: &[]awsiam.IManagedPolicy{
			awsiam.ManagedPolicy_FromAwsManagedPolicyName(jsii.String("AmazonSSMManagedInstanceCore")),
		},
	})

	role.AddToPolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Actions: jsii.Strings("ssm:GetParameter", "ssm:PutParameter"),
		Resources: jsii.Strings(fmt.Sprintf("arn:aws:ssm:%s:%s:parameter/game-password-*",
			*stack.Region(), *stack.Account())),
	}))
	role.AddToPolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Actions: jsii.Strings("s3:PutObject"),
		Resources: &[]*string{
			jsii.String(fmt.Sprintf("arn:aws:s3:::%s/status/*", *statusBucketName)),
			jsii.String(fmt.Sprintf("arn:aws:s3:::%s/worlds/*", *statusBucketName)),
		},
	}))

	return awsiam.NewInstanceProfile(stack, jsii.String("GameServerInstanceProfile"), &awsiam.InstanceProfileProps{
		Role: role,
	})
}

// addSecurityGroups gives each game its own group for its ports. Servers of the same game share one.
func addSecurityGroups(stack awscdk.Stack, vpc awsec2.IVpc) map[string]*string {
	ids := map[string]*string{}
	for _, template := range GameTemplates {
		group := awsec2.NewSecurityGroup(stack, jsii.String("SecurityGroup"+template.Game), &awsec2.SecurityGroupProps{
			Vpc:              vpc,
			Description:      jsii.String(template.DisplayName + " servers"),
			AllowAllOutbound: jsii.Bool(true),
		})
		for _, p := range template.TcpPorts {
			group.AddIngressRule(awsec2.Peer_AnyIpv4(),
				awsec2.Port_TcpRange(jsii.Number(p.From), jsii.Number(p.To)),
				jsii.String(template.DisplayName+" TCP"), jsii.Bool(false))
		}
		for _, p := range template.UdpPorts {
			group.AddIngressRule(awsec2.Peer_AnyIpv4(),
				awsec2.Port_UdpRange(jsii.Number(p.From), jsii.Number(p.To)),
				jsii.String(template.DisplayName+" UDP"), jsii.Bool(false))
		}
		ids[template.Game] = group.SecurityGroupId()
	}
	return ids
}

// addBackups creates the vault game server backups are kept in, and the role that writes them. What
// gets backed up, and when, is decided by the backup Lambda rather than by a plan: a plan can only
// schedule unconditionally and expire by age.
func addBackups(stack awscdk.Stack, config Config) (awsbackup.BackupVault, awsiam.IRole) {
	key := awskms.NewKey(stack, jsii.String("BackupKey"), &awskms.KeyProps{
		Description:       jsii.String("Encrypts game server backups"),
		EnableKeyRotation: jsii.Bool(true),
		//The recovery points are encrypted with this, so it outlives the stack that made it.
		RemovalPolicy: awscdk.RemovalPolicy_RETAIN,
	})

	vault := awsbackup.NewBackupVault(stack, jsii.String("BackupVault"), &awsbackup.BackupVaultProps{
		EncryptionKey: key,
		RemovalPolicy: awscdk.RemovalPolicy_RETAIN,
	})

	backupRole := awsiam.NewRole(stack, jsii.String("OnDemandBackupRole"), &awsiam.RoleProps{
		AssumedBy:   awsiam.NewServicePrincipal(jsii.String("backup.amazonaws.com"), nil),
		Description: jsii.String("Writes and restores game server backups"),
		ManagedPolicies: &[]awsiam.IManagedPolicy{
			awsiam.ManagedPolicy_FromAwsManagedPolicyName(jsii.String("service-role/AWSBackupServiceRolePolicyForBackup")),
			awsiam.ManagedPolicy_FromAwsManagedPolicyName(jsii.String("service-role/AWSBackupServiceRolePolicyForRestores")),
		},
	})

	return vault, backupRole
}

// addServerBackups backs up a server only when it has changed since its last backup, and keeps the
// most recent few. Both are decisions AWS Backup's own scheduling can't express.
func addServerBackups(stack awscdk.Stack, config Config, statusBucketName *string,
	vault awsbackup.BackupVault, backupRole awsiam.IRole) awslambda.IFunction {

	fn := awslambda.NewFunction(stack, jsii.String("BackupServersLambda"), &awslambda.FunctionProps{
		Runtime:     awslambda.Runtime_PROVIDED_AL2023(),
		Handler:     jsii.String("bootstrap"),
		Code:        awslambda.Code_FromAsset(jsii.String(config.BackupLambdaAssetDir), nil),
		MemorySize:  jsii.Number(128),
		Timeout:     awscdk.Duration_Minutes(jsii.Number(5)),
		Description: jsii.String("Backs up game servers that changed, and keeps the most recent few"),
		Environment: &map[string]*string{
			"backupVaultName": vault.BackupVaultName(),
			"backupRoleArn":   backupRole.RoleArn(),
			"statusBucket":    statusBucketName,
			"backupTag":       jsii.String(config.BackupTag),
			"backupValue":     jsii.String(config.BackupValue),
			"keepPerServer":   jsii.String(fmt.Sprintf("%d", config.KeepBackupsPerServer)),
			"supersededDays":  jsii.String(fmt.Sprintf("%d", config.SupersededBackupDays)),
			"accountId":       stack.Account(),
		},
	})

	fn.AddToRolePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("ec2:DescribeVolumes", "ec2:DescribeInstances"),
		Resources: jsii.Strings("*"),
	}))
	fn.AddToRolePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("backup:StartBackupJob", "backup:ListRecoveryPointsByBackupVault"),
		Resources: jsii.Strings(*vault.BackupVaultArn()),
	}))
	//Authorised against the recovery point as well as the vault. A volume's recovery point is a
	//snapshot, whose ARN carries no account id.
	fn.AddToRolePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Actions: jsii.Strings("backup:DeleteRecoveryPoint", "backup:UpdateRecoveryPointLifecycle"),
		Resources: &[]*string{
			vault.BackupVaultArn(),
			jsii.String(fmt.Sprintf("arn:aws:ec2:%s::image/*", *stack.Region())),
			jsii.String(fmt.Sprintf("arn:aws:ec2:%s::snapshot/*", *stack.Region())),
		},
	}))
	fn.AddToRolePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("s3:GetObject"),
		Resources: jsii.Strings(fmt.Sprintf("arn:aws:s3:::%s/status/*", *statusBucketName)),
	}))
	//Handed to AWS Backup, which does the reading of the instance and its volumes itself.
	fn.AddToRolePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("iam:PassRole"),
		Resources: &[]*string{backupRole.RoleArn()},
		Conditions: &map[string]interface{}{
			"StringEquals": map[string]string{"iam:PassedToService": "backup.amazonaws.com"},
		},
	}))

	awsevents.NewRule(stack, jsii.String("BackupSchedule"), &awsevents.RuleProps{
		Description: jsii.String("Decides each night which game servers need backing up"),
		Schedule: awsevents.Schedule_Cron(&awsevents.CronOptions{
			Hour: jsii.String("5"), Minute: jsii.String("0")}),
		Targets: &[]awsevents.IRuleTarget{awseventstargets.NewLambdaFunction(fn, nil)},
	})

	return fn
}

// addIdleShutdown stops servers nobody is playing. One schedule covers them all; a server carrying the
// pause tag is skipped.
func addIdleShutdown(stack awscdk.Stack, config Config, statusBucketName *string) {
	fn := awslambda.NewFunction(stack, jsii.String("IdleShutdownLambda"), &awslambda.FunctionProps{
		Runtime:     awslambda.Runtime_PYTHON_3_12(),
		Handler:     jsii.String("lambda_function.lambda_handler"),
		Code:        awslambda.Code_FromAsset(jsii.String(config.IdleShutdownAssetDir), nil),
		MemorySize:  jsii.Number(128),
		Timeout:     awscdk.Duration_Seconds(jsii.Number(30)),
		Description: jsii.String("Stops game servers that have had no players for two consecutive checks"),
		Environment: &map[string]*string{
			"statusBucket": statusBucketName,
			"managedTag":   jsii.String(config.ManagedTag),
			"managedValue": jsii.String(config.ManagedValue),
		},
	})

	fn.AddToRolePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("ec2:DescribeInstances"),
		Resources: jsii.Strings("*"),
	}))
	fn.AddToRolePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("ec2:StopInstances", "ec2:CreateTags", "ec2:DeleteTags"),
		Resources: jsii.Strings(fmt.Sprintf("arn:aws:ec2:%s:%s:instance/*", *stack.Region(), *stack.Account())),
		Conditions: &map[string]interface{}{
			"StringEquals": map[string]string{
				fmt.Sprintf("ec2:ResourceTag/%s", config.ManagedTag): config.ManagedValue,
			},
		},
	}))
	fn.AddToRolePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Actions:   jsii.Strings("s3:GetObject"),
		Resources: jsii.Strings(fmt.Sprintf("arn:aws:s3:::%s/status/*", *statusBucketName)),
	}))

	awsevents.NewRule(stack, jsii.String("IdleCheckSchedule"), &awsevents.RuleProps{
		Description: jsii.String("Checks every 15 minutes for servers with no players"),
		Schedule:    awsevents.Schedule_Rate(awscdk.Duration_Minutes(jsii.Number(15))),
		Targets:     &[]awsevents.IRuleTarget{awseventstargets.NewLambdaFunction(fn, nil)},
	})
}
