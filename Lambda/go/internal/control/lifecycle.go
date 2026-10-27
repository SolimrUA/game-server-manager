package control

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"personal-game-server-manager/lambda/internal/server"
	"personal-game-server-manager/lambda/internal/usererr"
)

func getInfo(context.Context, *Handler, Request, []server.Server) (any, error) {
	return "No action, just getting info", nil
}

func start(ctx context.Context, h *Handler, _ Request, servers []server.Server) (any, error) {
	ids := instanceIDs(servers)
	if _, err := h.ec2.StartInstances(ctx, &ec2.StartInstancesInput{InstanceIds: ids}); err != nil {
		return nil, fmt.Errorf("starting %s: %w", strings.Join(ids, ", "), err)
	}

	// The idle-check tag is on the instance, so it outlives a stop/start cycle: left in place, the
	// first idle check after this start counts as the second consecutive empty one and stops it again.
	h.clearIdleCheck(ctx, ids)

	if err := h.nameInDNS(ctx, servers); err != nil {
		log.Printf("started %s but couldn't update DNS: %v", strings.Join(ids, ", "), err)
		return "Server started, but DNS update failed - please wait a few minutes and try again " +
			"or check your hosted zone is setup correctly", nil
	}
	return "Started server and updated DNS successfully", nil
}

func stop(ctx context.Context, h *Handler, _ Request, servers []server.Server) (any, error) {
	ids := instanceIDs(servers)
	if _, err := h.ec2.StopInstances(ctx, &ec2.StopInstancesInput{InstanceIds: ids}); err != nil {
		return nil, fmt.Errorf("stopping %s: %w", strings.Join(ids, ", "), err)
	}

	// Cleared on the stop too, or a stopped server still reads "Idle - stopping soon".
	h.clearIdleCheck(ctx, ids)
	return "Stopped server", nil
}

// resize changes a stopped server's instance type, which is what it costs to run.
func resize(ctx context.Context, h *Handler, req Request, servers []server.Server) (any, error) {
	instanceType, known := h.config.InstanceTypesBySize[req.ReSizeType]
	if !known {
		return nil, usererr.Newf("%q isn't a size this control panel offers", req.ReSizeType)
	}

	for _, s := range servers {
		if s.State != string(ec2types.InstanceStateNameStopped) {
			return nil, usererr.New("Your server is not stopped. Please stop it and retry resizing")
		}
	}

	for _, s := range servers {
		_, err := h.ec2.ModifyInstanceAttribute(ctx, &ec2.ModifyInstanceAttributeInput{
			InstanceId:   aws.String(s.InstanceID),
			InstanceType: &ec2types.AttributeValue{Value: aws.String(instanceType)},
		})
		if err != nil {
			log.Printf("couldn't resize %s to %s: %v", s.InstanceID, instanceType, err)
			return nil, usererr.New("There was an issue resizing your server. Make sure your target " +
				"instance type is compatible (e.g. ARM based servers such as T3g servers cannot be " +
				"resized to x86 server types such as T3a servers)")
		}
	}
	return "Server has been resized - please note it is currently stopped.", nil
}

func backupNow(ctx context.Context, h *Handler, _ Request, servers []server.Server) (any, error) {
	var started, failed []string
	for _, s := range servers {
		if s.ServerID == "" {
			log.Printf("can't back up %s: no %s tag", s.InstanceID, server.TagServerID)
			failed = append(failed, s.Label())
			continue
		}
		if _, err := h.backups.Now(ctx, s.ServerID); err != nil {
			log.Printf("couldn't back up %s: %v", s.ServerID, err)
			failed = append(failed, s.ServerID)
			continue
		}
		started = append(started, s.ServerID)
	}

	switch {
	case len(started) > 0 && len(failed) > 0:
		return fmt.Sprintf("Backup started for %s, but failed for %s",
			strings.Join(started, ", "), strings.Join(failed, ", ")), nil
	case len(started) > 0:
		return "Backup started for " + strings.Join(started, ", "), nil
	default:
		return nil, fmt.Errorf("no backup could be started")
	}
}

func pauseIdleShutdown(ctx context.Context, h *Handler, _ Request, servers []server.Server) (any, error) {
	return h.setIdleShutdown(ctx, servers, true)
}

func resumeIdleShutdown(ctx context.Context, h *Handler, _ Request, servers []server.Server) (any, error) {
	return h.setIdleShutdown(ctx, servers, false)
}

// setIdleShutdown exempts a server from the idle-shutdown schedule, or puts it back under it. One
// schedule covers every server, so a server is exempted by tag.
func (h *Handler) setIdleShutdown(ctx context.Context, servers []server.Server, pause bool) (any, error) {
	var done []string
	for _, s := range servers {
		var err error
		if pause {
			_, err = h.ec2.CreateTags(ctx, &ec2.CreateTagsInput{
				Resources: []string{s.InstanceID},
				Tags: []ec2types.Tag{{
					Key:   aws.String(server.TagIdlePause),
					Value: aws.String(server.IdlePauseValue),
				}},
			})
		} else {
			_, err = h.ec2.DeleteTags(ctx, &ec2.DeleteTagsInput{
				Resources: []string{s.InstanceID},
				Tags:      []ec2types.Tag{{Key: aws.String(server.TagIdlePause)}},
			})
		}
		if err != nil {
			log.Printf("couldn't change auto-shutdown for %s: %v", s.Label(), err)
			continue
		}
		done = append(done, s.Label())
	}

	if len(done) == 0 {
		return nil, fmt.Errorf("no server's auto-shutdown could be changed")
	}
	verb := "resumed"
	if pause {
		verb = "paused"
	}
	return fmt.Sprintf("Auto-shutdown %s for %s", verb, strings.Join(done, ", ")), nil
}

// clearIdleCheck forgets that a server was found empty. Logged rather than failed: the start or stop
// it accompanies has already happened.
func (h *Handler) clearIdleCheck(ctx context.Context, instanceIDs []string) {
	_, err := h.ec2.DeleteTags(ctx, &ec2.DeleteTagsInput{
		Resources: instanceIDs,
		Tags:      []ec2types.Tag{{Key: aws.String(server.TagIdleCheck)}},
	})
	if err != nil {
		log.Printf("couldn't clear the %s tag: %v", server.TagIdleCheck, err)
	}
}

func instanceIDs(servers []server.Server) []string {
	ids := make([]string, 0, len(servers))
	for _, s := range servers {
		ids = append(ids, s.InstanceID)
	}
	return ids
}
