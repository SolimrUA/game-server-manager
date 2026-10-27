// Command updatedns points a game server's DNS record at the address it currently has.
//
// A freshly launched instance has no public address for a few seconds. Rather than wait here, this
// reports NoAddressYet and lets the state machine that invokes it retry - the wait costs state
// machine time instead of billed Lambda runtime.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
)

// recordTTL is short because the address changes every time the server is started.
const recordTTL = 5

// NoAddressYet reports an instance that hasn't been given a public address yet. The state machine
// retries on this error's name, so the two have to agree.
type NoAddressYet struct {
	InstanceID string
}

func (e *NoAddressYet) Error() string {
	return "no public IP address on " + e.InstanceID + " yet"
}

type request struct {
	InstanceID   string `json:"instanceId"`
	HostedZoneID string `json:"hostedZoneId"`
	DomainName   string `json:"domainName"`
}

type handler struct {
	ec2     *ec2.Client
	route53 *route53.Client
}

func main() {
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatalf("loading AWS config: %v", err)
	}

	h := &handler{ec2: ec2.NewFromConfig(cfg), route53: route53.NewFromConfig(cfg)}
	lambda.Start(h.handle)
}

func (h *handler) handle(ctx context.Context, req request) error {
	address, err := h.publicAddress(ctx, req.InstanceID)
	if err != nil {
		return err
	}

	log.Printf("pointing %s at %s", req.DomainName, address)
	return h.point(ctx, req, address)
}

func (h *handler) publicAddress(ctx context.Context, instanceID string) (string, error) {
	out, err := h.ec2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{instanceID}})
	if err != nil {
		return "", fmt.Errorf("describing %s: %w", instanceID, err)
	}

	for _, reservation := range out.Reservations {
		for _, instance := range reservation.Instances {
			if address := aws.ToString(instance.PublicIpAddress); address != "" {
				return address, nil
			}
		}
	}
	return "", &NoAddressYet{InstanceID: instanceID}
}

func (h *handler) point(ctx context.Context, req request, address string) error {
	_, err := h.route53.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(req.HostedZoneID),
		ChangeBatch: &r53types.ChangeBatch{
			Comment: aws.String("Auto updated by game server DNS handler"),
			Changes: []r53types.Change{{
				Action: r53types.ChangeActionUpsert,
				ResourceRecordSet: &r53types.ResourceRecordSet{
					Name:            aws.String(req.DomainName),
					Type:            r53types.RRTypeA,
					TTL:             aws.Int64(recordTTL),
					ResourceRecords: []r53types.ResourceRecord{{Value: aws.String(address)}},
				},
			}},
		},
	})
	if err != nil {
		return fmt.Errorf("pointing %s at %s: %w", req.DomainName, address, err)
	}
	return nil
}
