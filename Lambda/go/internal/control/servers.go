package control

import (
	"context"
	"fmt"
	"log"
	"time"

	"personal-game-server-manager/lambda/internal/access"

	"personal-game-server-manager/lambda/internal/provision"
	"personal-game-server-manager/lambda/internal/server"
	"personal-game-server-manager/lambda/internal/usererr"
)

func serverOptions(ctx context.Context, h *Handler, req Request, servers []server.Server) (any, error) {
	options := h.provisioner.Options(ctx)
	options.GameVersions = h.gameVersions(ctx, servers)
	if access.IsAdmin(access.Groups(req.Groups)) {
		return options, nil
	}

	// Restoring a backup copies that world onto a server the restorer owns, so the list is narrowed
	// to the servers they already reach.
	granted, err := h.grants.RolesFor(ctx, req.Sub)
	if err != nil {
		return nil, err
	}
	theirs := make([]provision.Backup, 0, len(options.Backups))
	for _, backup := range options.Backups {
		if granted.Roles[backup.ServerID] != "" {
			theirs = append(theirs, backup)
		}
	}
	options.Backups = theirs
	return options, nil
}

// gameVersions is the version to suggest for each game. Nothing records a version to create a server
// at, so the fleet is the only thing that knows one.
func (h *Handler) gameVersions(ctx context.Context, servers []server.Server) map[string]string {
	versions := map[string]string{}
	for _, s := range servers {
		if s.GameName == "" || versions[s.GameName] != "" {
			continue
		}
		if version := s.GameStatus.Version(); version != "" {
			versions[s.GameName] = version
			continue
		}
		if version := h.versionLastReported(ctx, s); version != "" {
			versions[s.GameName] = version
		}
	}
	return versions
}

// versionLastReported reads the version out of a stopped server's last status, which is otherwise
// too stale to show: the server comes back on the version it stopped at.
func (h *Handler) versionLastReported(ctx context.Context, s server.Server) string {
	left, err := h.status.Game(ctx, s.StatusKey(), time.Time{})
	if err != nil {
		log.Printf("no version left behind by %s: %v", s.Label(), err)
		return ""
	}
	return left.Version()
}

// createServer builds a server, and hands a user who is not an admin ownership of the one they just
// made. Their permission to make it is spent first: spending it is what stops two requests at once
// from getting two servers out of one permit.
func createServer(ctx context.Context, h *Handler, req Request, _ []server.Server) (any, error) {
	isAdmin := access.IsAdmin(access.Groups(req.Groups))
	if !isAdmin {
		spent, err := h.grants.ConsumePermit(ctx, req.Sub)
		if err != nil {
			return nil, err
		}
		if !spent {
			return nil, usererr.New("You don't have permission to create a server")
		}
	}

	message, err := h.provisioner.Create(ctx, provision.CreateRequest{
		Game:         req.Game,
		ServerID:     req.ServerID,
		InstanceType: req.InstanceType,
		GameVersion:  req.GameVersion,
		VolumeSize:   req.VolumeSize,
		SnapshotID:   req.SnapshotID,
	})
	if err != nil {
		if !isAdmin {
			// A server that was never made should not cost them their one permit.
			if restoreErr := h.grants.SetPermit(ctx, req.Sub, true, ""); restoreErr != nil {
				log.Printf("couldn't give %s their create permit back: %v", req.Sub, restoreErr)
			}
		}
		return nil, err
	}

	h.grantAccessToNewServer(ctx, req, isAdmin)
	return message, nil
}

// grantAccessToNewServer cannot fail the request: the server exists by now, and an admin can grant
// what didn't get written.
func (h *Handler) grantAccessToNewServer(ctx context.Context, req Request, isAdmin bool) {
	// The owner grant below writes the same item, so the defaults have to go first or the creator is
	// left with the default role.
	if err := h.grants.ApplyDefaultsTo(ctx, req.ServerID); err != nil {
		log.Printf("couldn't apply the access defaults to %s: %v", req.ServerID, err)
	}
	if isAdmin {
		return
	}
	owner := []access.Change{{UserSub: req.Sub, ServerID: req.ServerID, Role: string(access.Owner)}}
	if err := h.grants.Save(ctx, owner, req.Sub); err != nil {
		log.Printf("couldn't make %s the owner of %s: %v", req.Sub, req.ServerID, err)
	}
}

func deleteServer(ctx context.Context, h *Handler, _ Request, servers []server.Server) (any, error) {
	if len(servers) == 0 {
		return nil, usererr.New("No matching instance found")
	}

	target := servers[0]
	message, err := h.provisioner.Delete(ctx, target)
	if err != nil {
		return nil, err
	}

	// The instance is already terminated, so this is reported rather than failed.
	if err := h.grants.ForgetServer(ctx, target.ServerID); err != nil {
		log.Printf("couldn't forget the grants on %s: %v", target.ServerID, err)
		return message + " Who had access to it is still recorded, which needs looking at.", nil
	}
	return message, nil
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
