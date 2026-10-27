// Package remote runs shell commands on a game server through SSM.
package remote

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// shellDocument is the SSM document that runs a list of shell commands.
const shellDocument = "AWS-RunShellScript"

// SSM runs commands on instances.
type SSM interface {
	SendCommand(context.Context, *ssm.SendCommandInput, ...func(*ssm.Options)) (*ssm.SendCommandOutput, error)
	GetCommandInvocation(context.Context, *ssm.GetCommandInvocationInput, ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error)
}

// Runner sends commands to a server and reads back what they printed.
type Runner struct {
	ssm SSM
}

func NewRunner(client SSM) *Runner {
	return &Runner{ssm: client}
}

// Result is what a finished command printed.
type Result struct {
	Succeeded bool
	Stdout    string
	Stderr    string
}

// Message is the command's own account of what went wrong, for showing to the caller.
func (r Result) Message() string {
	for _, output := range []string{r.Stderr, r.Stdout} {
		if output != "" {
			return truncate(output, 300)
		}
	}
	return "unknown error"
}

// Start sends commands and returns the command id without waiting. timeout is how long SSM itself
// allows the command to run, which for a multi-gigabyte world is much longer than any caller waits.
func (r *Runner) Start(ctx context.Context, instanceID string, commands []string, timeout time.Duration) (string, error) {
	out, err := r.ssm.SendCommand(ctx, &ssm.SendCommandInput{
		InstanceIds:    []string{instanceID},
		DocumentName:   aws.String(shellDocument),
		Parameters:     map[string][]string{"commands": commands},
		TimeoutSeconds: aws.Int32(int32(timeout.Seconds())),
	})
	if err != nil {
		return "", fmt.Errorf("sending a command to %s: %w", instanceID, err)
	}
	if out.Command == nil {
		return "", fmt.Errorf("sending a command to %s: no command id returned", instanceID)
	}
	return aws.ToString(out.Command.CommandId), nil
}

// Run sends commands and waits for them to finish. Safe to wait on, unlike a world download: a
// console command or file edit finishes in seconds, and anything slow is backgrounded inside the
// command text itself.
func (r *Runner) Run(ctx context.Context, instanceID string, commands []string) (Result, error) {
	commandID, err := r.Start(ctx, instanceID, commands, time.Minute)
	if err != nil {
		return Result{}, err
	}

	for attempt := 0; attempt < 10; attempt++ {
		result, done, err := r.poll(ctx, commandID, instanceID)
		if err != nil {
			return Result{}, err
		}
		if done {
			return result, nil
		}
		select {
		case <-ctx.Done():
			return Result{}, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return Result{}, errors.New("timed out waiting for the command to complete")
}

// Poll reports a command's progress without waiting for it.
func (r *Runner) Poll(ctx context.Context, commandID, instanceID string) (Result, bool, error) {
	return r.poll(ctx, commandID, instanceID)
}

// poll reads one invocation. done is false while the command is still registering or running.
func (r *Runner) poll(ctx context.Context, commandID, instanceID string) (Result, bool, error) {
	out, err := r.ssm.GetCommandInvocation(ctx, &ssm.GetCommandInvocationInput{
		CommandId:  aws.String(commandID),
		InstanceId: aws.String(instanceID),
	})
	if err != nil {
		// Briefly expected right after the command is sent, before it registers on the instance.
		var missing *ssmtypes.InvocationDoesNotExist
		if errors.As(err, &missing) {
			return Result{}, false, nil
		}
		return Result{}, false, fmt.Errorf("reading command %s: %w", commandID, err)
	}

	switch out.Status {
	case ssmtypes.CommandInvocationStatusPending,
		ssmtypes.CommandInvocationStatusInProgress,
		ssmtypes.CommandInvocationStatusDelayed:
		return Result{}, false, nil
	}

	return Result{
		Succeeded: out.Status == ssmtypes.CommandInvocationStatusSuccess,
		Stdout:    aws.ToString(out.StandardOutputContent),
		Stderr:    aws.ToString(out.StandardErrorContent),
	}, true, nil
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit]
}

// Quote renders a string as a single shell word, so a value the caller supplied cannot run as
// anything but an argument.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
