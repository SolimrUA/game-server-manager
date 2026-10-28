package control

import (
	"context"
	"fmt"
	"strings"

	"personal-game-server-manager/lambda/internal/server"
	"personal-game-server-manager/lambda/internal/usererr"
)

// GamePassword answers the password commands. It carries the server it belongs to so the control
// panel can tell which row a late answer is for.
type GamePassword struct {
	ServerID string `json:"serverId"`
	Password string `json:"password"`
}

// minPasswordLength is Valheim's: it refuses to start on anything shorter. Vintage Story is held to
// the same limit.
const minPasswordLength = 5

func gamePassword(ctx context.Context, h *Handler, _ Request, servers []server.Server) (any, error) {
	if len(servers) == 0 {
		return nil, usererr.New("No matching instance found")
	}
	s := servers[0]
	password, err := h.provisioner.Password(ctx, s.ServerID)
	if err != nil {
		return nil, err
	}
	return GamePassword{ServerID: s.ServerID, Password: password}, nil
}

func setGamePassword(ctx context.Context, h *Handler, req Request, servers []server.Server) (any, error) {
	if len(servers) == 0 {
		return nil, usererr.New("No matching instance found")
	}
	s := servers[0]

	// A pasted password tends to arrive with a newline.
	password := strings.TrimSpace(req.Body.Password)
	if len(password) < minPasswordLength {
		return nil, usererr.New(fmt.Sprintf(
			"A join password needs at least %d characters", minPasswordLength))
	}
	if err := h.provisioner.SetPassword(ctx, s.ServerID, password); err != nil {
		return nil, err
	}
	return GamePassword{ServerID: s.ServerID, Password: password}, nil
}
