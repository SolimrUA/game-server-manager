package control

import (
	"context"
	"fmt"
	"strings"

	"personal-game-server-manager/lambda/internal/server"
	"personal-game-server-manager/lambda/internal/usererr"
)

// ServerNamed answers the rename command. It carries the server it belongs to so the control panel
// can tell which row a late answer is for.
type ServerNamed struct {
	ServerID   string `json:"serverId"`
	ServerName string `json:"serverName"`
}

const maxServerNameLength = 64

func setServerName(ctx context.Context, h *Handler, req Request, servers []server.Server) (any, error) {
	if len(servers) == 0 {
		return nil, usererr.New("No matching instance found")
	}
	s := servers[0]

	name := strings.TrimSpace(req.Body.ServerName)
	if name == "" {
		return nil, usererr.New("A server name can't be empty")
	}
	if len(name) > maxServerNameLength {
		return nil, usererr.New(fmt.Sprintf(
			"A server name can be at most %d characters", maxServerNameLength))
	}
	// A name reaches the game through its config file, one line of which is rewritten in place.
	if strings.ContainsAny(name, "\r\n") {
		return nil, usererr.New("A server name can't span lines")
	}
	if err := h.provisioner.SetServerName(ctx, s.ServerID, name); err != nil {
		return nil, err
	}
	return ServerNamed{ServerID: s.ServerID, ServerName: name}, nil
}
