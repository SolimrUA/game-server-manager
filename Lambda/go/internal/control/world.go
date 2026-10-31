package control

import (
	"context"
	"encoding/json"
	"errors"
	"log"

	"personal-game-server-manager/lambda/internal/games"
	"personal-game-server-manager/lambda/internal/server"
	"personal-game-server-manager/lambda/internal/usererr"
)

// WorldCreated answers the new-world command. It carries the server it belongs to so the control
// panel can tell which row a late answer is for.
type WorldCreated struct {
	ServerID  string `json:"serverId"`
	WorldName string `json:"worldName"`
}

func worldDefaults(_ context.Context, _ *Handler, _ Request, servers []server.Server) (any, error) {
	game, err := gameFor(servers)
	if err != nil {
		return nil, err
	}
	defaults, err := game.WorldDefaults()
	if errors.Is(err, games.ErrUnsupported) {
		return nil, usererr.New("This game can't be given a new world from the control panel")
	}
	if err != nil {
		return nil, err
	}
	return defaults, nil
}

// newWorld records the world the server generates the next time its game starts. The world it is on
// now is left on the volume, so this adds a world rather than replacing one.
func newWorld(ctx context.Context, h *Handler, req Request, servers []server.Server) (any, error) {
	game, err := gameFor(servers)
	if err != nil {
		return nil, err
	}
	s := servers[0]

	world, err := game.NewWorld(games.NewWorldRequest{
		Name: req.Body.WorldName,
		Body: req.Body.WorldConfig,
		Seed: req.Body.WorldSeed,
	})
	if errors.Is(err, games.ErrUnsupported) {
		return nil, usererr.New("This game can't be given a new world from the control panel")
	}
	if err != nil {
		return nil, err
	}

	stored, err := json.Marshal(world)
	if err != nil {
		return nil, err
	}
	if err := h.provisioner.SetWorld(ctx, s.ServerID, string(stored)); err != nil {
		return nil, err
	}
	return WorldCreated{ServerID: s.ServerID, WorldName: world.Name}, nil
}

func gameFor(servers []server.Server) (games.Game, error) {
	if len(servers) == 0 {
		return nil, usererr.New("No matching instance found")
	}
	return games.For(servers[0].GameName)
}

// setPlayerRole gives a player one of the roles the server's own config defines. The console it goes
// through is the running game's, so unlike a new world this takes effect at once - and needs the
// game up.
func setPlayerRole(ctx context.Context, h *Handler, req Request, servers []server.Server) (any, error) {
	s, game, err := only(servers)
	if err != nil {
		return nil, err
	}

	commands, err := game.SetPlayerRole(req.Body.PlayerName, req.Body.PlayerRole)
	if err != nil {
		return nil, unsupported(err, "Setting a player's role", s.GameName)
	}

	result, err := h.runner.Run(ctx, s.InstanceID, commands)
	if err != nil {
		return nil, err
	}
	log.Printf("setPlayerRole on %s: success=%t stdout=%q stderr=%q",
		s.Label(), result.Succeeded, result.Stdout, result.Stderr)

	if !result.Succeeded {
		return nil, usererr.New("Couldn't set that player's role: " + result.Message())
	}
	return req.Body.PlayerName + " now holds the " + req.Body.PlayerRole + " role", nil
}
