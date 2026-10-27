package control

import (
	"context"
	"fmt"
	"log"

	"personal-game-server-manager/lambda/internal/games"
	"personal-game-server-manager/lambda/internal/server"
	"personal-game-server-manager/lambda/internal/usererr"
)

// installMod installs a mod but deliberately doesn't restart the game: an admin installing several
// in a row shouldn't pay a restart each time, and asks for one when ready.
func installMod(ctx context.Context, h *Handler, req Request, servers []server.Server) (any, error) {
	s, game, err := only(servers)
	if err != nil {
		return nil, err
	}
	if req.ModRef == "" {
		return nil, usererr.New("No mod was named to install")
	}

	// An admin names a version to reach a release that lists only older ones. Otherwise the mod has
	// to match what the server reports running.
	version := req.ModGameVersion
	if version == "" {
		version = s.GameStatus.Version()
	}

	commands, err := game.InstallMod(ctx, games.ModRequest{
		Ref:      req.ModRef,
		Version:  version,
		DataPath: s.DataPath,
	})
	if err != nil {
		return nil, err
	}

	result, err := h.runner.Run(ctx, s.InstanceID, commands)
	if err != nil {
		return nil, err
	}
	// Logged either way: injecting a console command always exits 0 whether or not the game acted on
	// it, so this output is the only evidence of what the game printed back.
	log.Printf("installMod %s on %s: success=%t stdout=%q stderr=%q",
		req.ModRef, s.Label(), result.Succeeded, result.Stdout, result.Stderr)

	if !result.Succeeded {
		return nil, usererr.New("Couldn't install mod: " + result.Message())
	}
	return "Mod install requested - restart the server (once you're ready) to load it", nil
}

// deleteMod removes a mod, and doesn't restart the game for the same reason installMod doesn't.
func deleteMod(ctx context.Context, h *Handler, req Request, servers []server.Server) (any, error) {
	s, game, err := only(servers)
	if err != nil {
		return nil, err
	}
	if req.ModName == "" {
		return nil, usererr.New("No mod was named to delete")
	}

	modRequest := games.ModRequest{Name: req.ModName, DataPath: s.DataPath}
	commands, err := game.DeleteMod(modRequest)
	if err != nil {
		return nil, err
	}

	result, err := h.runner.Run(ctx, s.InstanceID, commands)
	if err != nil {
		return nil, err
	}
	log.Printf("deleteMod %s on %s: success=%t stdout=%q stderr=%q",
		req.ModName, s.Label(), result.Succeeded, result.Stdout, result.Stderr)

	if !result.Succeeded {
		if known := game.DeleteFailure(modRequest, result.Stdout); known != nil {
			return nil, known
		}
		return nil, usererr.New("Couldn't delete mod: " + result.Message())
	}
	return "Mod removed - restart the server (once you're ready) for the change to take effect", nil
}

func restartGame(ctx context.Context, h *Handler, _ Request, servers []server.Server) (any, error) {
	s, game, err := only(servers)
	if err != nil {
		return nil, err
	}

	commands, err := game.Restart(s.DataPath)
	if err != nil {
		return nil, unsupported(err, "Restart", s.GameName)
	}

	result, err := h.runner.Run(ctx, s.InstanceID, commands)
	if err != nil {
		return nil, err
	}
	log.Printf("restartGame on %s: success=%t stdout=%q stderr=%q",
		s.Label(), result.Succeeded, result.Stdout, result.Stderr)

	if !result.Succeeded {
		return nil, usererr.New("Couldn't restart the game: " + result.Message())
	}
	return "Restart requested - the server will be back in a minute or two", nil
}

// updateGame puts the newest release on the server. The update restarts the game itself, so no
// separate restart is needed after it.
func updateGame(ctx context.Context, h *Handler, _ Request, servers []server.Server) (any, error) {
	s, game, err := only(servers)
	if err != nil {
		return nil, err
	}

	commands, err := game.Update(s.DataPath)
	if err != nil {
		return nil, unsupported(err, "Update", s.GameName)
	}

	result, err := h.runner.Run(ctx, s.InstanceID, commands)
	if err != nil {
		return nil, err
	}
	log.Printf("updateGame on %s: success=%t stdout=%q stderr=%q",
		s.Label(), result.Succeeded, result.Stdout, result.Stderr)

	if !result.Succeeded {
		return nil, usererr.New("Couldn't update the game: " + result.Message())
	}
	return "Update requested - the server will be back in a few minutes once it's downloaded and " +
		"restarted (check /tmp/vs-update.log on the instance if it doesn't come back)", nil
}

// only returns the single server a per-server command acts on, and how to administer its game.
func only(servers []server.Server) (server.Server, games.Game, error) {
	if len(servers) == 0 {
		return server.Server{}, nil, usererr.New("No matching instance found")
	}

	s := servers[0]
	if s.GameName == "" || s.DataPath == "" {
		return server.Server{}, nil, usererr.Newf(
			"%s is missing its game-name or data-path tag, so there's no way to tell what it runs", s.Label())
	}

	game, err := games.For(s.GameName)
	if err != nil {
		return server.Server{}, nil, err
	}
	return s, game, nil
}

// unsupported turns a game's "I don't do that" into something the caller can read.
func unsupported(err error, what, gameName string) error {
	if err == games.ErrUnsupported {
		return usererr.Newf("%s isn't supported for %s yet", what, gameName)
	}
	return fmt.Errorf("%s for %s: %w", what, gameName, err)
}
