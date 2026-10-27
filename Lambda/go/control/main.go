// Command control answers the control panel's requests: it reports what each game server is doing,
// and acts on the ones the caller is allowed to act on.
//
// Every endpoint of the control API invokes this one function and is told which command to run by
// the API's own mapping template, so what the API offers is the command table in internal/control.
package main

import (
	"context"
	"log"

	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/backup"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"personal-game-server-manager/lambda/internal/access"
	"personal-game-server-manager/lambda/internal/control"
	"personal-game-server-manager/lambda/internal/provision"
	"personal-game-server-manager/lambda/internal/remote"
	"personal-game-server-manager/lambda/internal/status"
)

// instanceSizes are the sizes the resize action offers. The allowlist this Lambda's own IAM policy
// conditions RunInstances on has to agree with these.
var instanceSizes = []string{"micro", "small", "medium", "large"}

func main() {
	var vars env
	settings := vars.read()
	if err := vars.Err(); err != nil {
		log.Fatal(err)
	}

	cfg, err := awsconfig.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatalf("loading AWS config: %v", err)
	}

	s3Client := s3.NewFromConfig(cfg)
	presigner := s3.NewPresignClient(s3Client)
	ec2Client := ec2.NewFromConfig(cfg)
	ssmClient := ssm.NewFromConfig(cfg)

	statusReader := status.NewReader(s3Client, settings.statusBucket)
	launcher := afterLaunch{sfn: sfn.NewFromConfig(cfg), stateMachineArn: settings.stateMachineArn}

	h := control.NewHandler(control.Dependencies{
		EC2:       ec2Client,
		Backup:    backup.NewFromConfig(cfg),
		Backups:   backupLambda{lambda: awslambda.NewFromConfig(cfg), name: settings.backupLambdaName},
		Status:    statusReader,
		Runner:    remote.NewRunner(ssmClient),
		Downloads: downloadURLs{presigner: presigner},
		Grants:    access.NewStore(dynamodb.NewFromConfig(cfg), settings.accessTable),
		Directory: access.NewDirectory(cognitoidentityprovider.NewFromConfig(cfg), settings.userPoolID),
		Provisioner: provision.New(ec2Client, ssmClient, statusReader,
			installReports{presigner: presigner}, launcher, settings.provision),
		Launcher: launcher,
		Config:   settings.control,
	})

	lambda.Start(h.Handle)
}
