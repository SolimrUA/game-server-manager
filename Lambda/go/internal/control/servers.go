package control

import (
	"context"
	"fmt"

	"personal-game-server-manager/lambda/internal/provision"
	"personal-game-server-manager/lambda/internal/server"
	"personal-game-server-manager/lambda/internal/usererr"
)

func serverOptions(ctx context.Context, h *Handler, _ Request, _ []server.Server) (any, error) {
	return h.provisioner.Options(ctx), nil
}

func createServer(ctx context.Context, h *Handler, req Request, _ []server.Server) (any, error) {
	return h.provisioner.Create(ctx, provision.CreateRequest{
		Game:         req.Game,
		ServerID:     req.ServerID,
		InstanceType: req.InstanceType,
		GameVersion:  req.GameVersion,
		VolumeSize:   req.VolumeSize,
		SnapshotID:   req.SnapshotID,
	})
}

func deleteServer(ctx context.Context, h *Handler, _ Request, servers []server.Server) (any, error) {
	if len(servers) == 0 {
		return nil, usererr.New("No matching instance found")
	}
	return h.provisioner.Delete(ctx, servers[0])
}

// nameInDNS points each server's record at the address it has just been given. A freshly started
// instance has no address for a moment, so the waiting is a state machine's job rather than this
// function's.
func (h *Handler) nameInDNS(ctx context.Context, servers []server.Server) error {
	for _, s := range servers {
		if !s.HasDomain() {
			continue
		}
		if err := h.launcher.AfterLaunch(ctx, s.InstanceID, s.DomainName, s.HostedZoneID); err != nil {
			return fmt.Errorf("naming %s: %w", s.DomainName, err)
		}
	}
	return nil
}
