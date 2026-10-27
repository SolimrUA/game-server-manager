// Command idleshutdown stops game servers nobody is playing on.
//
// Two consecutive empty checks stop a server, so the first one only records that it was empty. The
// record is a tag on the instance rather than anything held here, because this runs on a schedule
// with no memory of its own.
package main

import (
	"context"
	"errors"
	"log"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"personal-game-server-manager/lambda/internal/env"
	"personal-game-server-manager/lambda/internal/server"
	"personal-game-server-manager/lambda/internal/status"
)

type handler struct {
	ec2    *ec2.Client
	status *status.Reader

	managedTag   string
	managedValue string
}

func main() {
	var vars env.Loader
	h := &handler{
		managedTag:   vars.Require("managedTag"),
		managedValue: vars.Require("managedValue"),
	}
	statusBucket := vars.Require("statusBucket")
	if err := vars.Err(); err != nil {
		log.Fatal(err)
	}

	cfg, err := awsconfig.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatalf("loading AWS config: %v", err)
	}

	h.ec2 = ec2.NewFromConfig(cfg)
	h.status = status.NewReader(s3.NewFromConfig(cfg), statusBucket)
	lambda.Start(h.handle)
}

func (h *handler) handle(ctx context.Context) error {
	instances, err := h.running(ctx)
	if err != nil {
		return err
	}

	var failures []error
	for _, instance := range instances {
		if err := h.check(ctx, instance); err != nil {
			// One server's failure must not stop the others being checked.
			log.Printf("%s: %v", aws.ToString(instance.InstanceId), err)
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (h *handler) running(ctx context.Context) ([]ec2types.Instance, error) {
	out, err := h.ec2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("tag:" + h.managedTag), Values: []string{h.managedValue}},
			{Name: aws.String("instance-state-name"), Values: []string{"running"}},
		},
	})
	if err != nil {
		return nil, err
	}

	var instances []ec2types.Instance
	for _, reservation := range out.Reservations {
		instances = append(instances, reservation.Instances...)
	}
	return instances, nil
}

// check stops a server that has been empty twice, and records the first empty check.
func (h *handler) check(ctx context.Context, instance ec2types.Instance) error {
	instanceID := aws.ToString(instance.InstanceId)
	tags := server.TagsOf(instance)

	if tags[server.TagIdlePause] == server.IdlePauseValue {
		return nil
	}

	serverID := tags[server.TagServerID]
	if serverID == "" {
		log.Printf("skipping %s: no %s tag", instanceID, server.TagServerID)
		return nil
	}

	game, err := h.status.Game(ctx, serverID, aws.ToTime(instance.LaunchTime))
	if err != nil {
		// Whether anyone is playing can't be told. A server that doesn't report is left running
		// rather than stopped on a guess.
		log.Printf("%s: leaving it running, no status to read: %v", serverID, err)
		return nil
	}

	players, _ := game.Players()
	// Covers both nobody playing and a game that isn't answering.
	emptyNow := players == 0
	wasEmpty := tags[server.TagIdleCheck] == "true"

	switch {
	case emptyNow && wasEmpty:
		log.Printf("%s: empty twice, stopping", serverID)
		if _, err := h.ec2.StopInstances(ctx, &ec2.StopInstancesInput{InstanceIds: []string{instanceID}}); err != nil {
			return err
		}
		// Cleared on the stop itself, or a stopped server still reads "Idle - stopping soon".
		return h.clearIdleCheck(ctx, instanceID)
	case emptyNow:
		_, err := h.ec2.CreateTags(ctx, &ec2.CreateTagsInput{
			Resources: []string{instanceID},
			Tags: []ec2types.Tag{{
				Key:   aws.String(server.TagIdleCheck),
				Value: aws.String("true"),
			}},
		})
		return err
	case wasEmpty:
		return h.clearIdleCheck(ctx, instanceID)
	}
	return nil
}

func (h *handler) clearIdleCheck(ctx context.Context, instanceID string) error {
	_, err := h.ec2.DeleteTags(ctx, &ec2.DeleteTagsInput{
		Resources: []string{instanceID},
		Tags:      []ec2types.Tag{{Key: aws.String(server.TagIdleCheck)}},
	})
	return err
}
