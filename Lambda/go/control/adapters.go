package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sfn"

	"personal-game-server-manager/lambda/internal/provision"
)

// The AWS SDK's own shapes don't match what the control panel's packages ask for, so each is wrapped
// here rather than letting an SDK type reach them.

// backupLambda asks the backup Lambda for a backup, so that taking one, expiring the one it
// supersedes and pruning the rest happen the same way as they do each night.
type backupLambda struct {
	lambda *awslambda.Client
	name   string
}

func (b backupLambda) Now(ctx context.Context, serverID string) (string, error) {
	payload, err := json.Marshal(map[string]any{"serverId": serverID, "force": true})
	if err != nil {
		return "", err
	}

	out, err := b.lambda.Invoke(ctx, &awslambda.InvokeInput{
		FunctionName:   aws.String(b.name),
		InvocationType: lambdatypes.InvocationTypeRequestResponse,
		Payload:        payload,
	})
	if err != nil {
		return "", fmt.Errorf("asking %s to back up %s: %w", b.name, serverID, err)
	}
	if out.FunctionError != nil {
		return "", fmt.Errorf("backing up %s: %s", serverID, string(out.Payload))
	}

	var message string
	if err := json.Unmarshal(out.Payload, &message); err != nil {
		return string(out.Payload), nil
	}
	return message, nil
}

// downloadURLs signs the URL a finished world archive is fetched from.
type downloadURLs struct {
	presigner *s3.PresignClient
}

// downloadValidity is how long a signed world download stays usable - long enough to start the
// download, not long enough for the link to be worth passing on.
const downloadValidity = 15 * time.Minute

func (d downloadURLs) PresignGetObject(ctx context.Context, bucket, key, filename string) (string, error) {
	request, err := d.presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket:                     aws.String(bucket),
		Key:                        aws.String(key),
		ResponseContentDisposition: aws.String(fmt.Sprintf("attachment; filename=%q", filename)),
	}, s3.WithPresignExpires(downloadValidity))
	if err != nil {
		return "", err
	}
	return request.URL, nil
}

// installReports signs the URLs a new server's installer reports its outcome and log to.
type installReports struct {
	presigner *s3.PresignClient
}

// installValidity has to outlast the install itself, which takes minutes and may retry.
const installValidity = 2 * time.Hour

func (i installReports) PresignPutObject(ctx context.Context, input *s3.PutObjectInput,
	opts ...func(*s3.PresignOptions)) (*provision.PresignedURL, error) {

	request, err := i.presigner.PresignPutObject(ctx, input, s3.WithPresignExpires(installValidity))
	if err != nil {
		return nil, err
	}
	return &provision.PresignedURL{URL: request.URL}, nil
}

// afterLaunch starts the state machine that finishes setting a server up once it has an address.
type afterLaunch struct {
	sfn             *sfn.Client
	stateMachineArn string
}

func (a afterLaunch) AfterLaunch(ctx context.Context, instanceID, domainName, hostedZoneID string) error {
	input, err := json.Marshal(map[string]string{
		"instanceId":   instanceID,
		"domainName":   domainName,
		"hostedZoneId": hostedZoneID,
	})
	if err != nil {
		return err
	}

	_, err = a.sfn.StartExecution(ctx, &sfn.StartExecutionInput{
		StateMachineArn: aws.String(a.stateMachineArn),
		Input:           aws.String(string(input)),
	})
	if err != nil {
		return fmt.Errorf("starting the after-launch steps for %s: %w", instanceID, err)
	}
	return nil
}
