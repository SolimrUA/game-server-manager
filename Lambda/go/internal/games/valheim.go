package games

import (
	"context"
	"strings"

	"personal-game-server-manager/lambda/internal/remote"
	"personal-game-server-manager/lambda/internal/usererr"
)

type valheim struct{}

// vhController is the one entry point the control panel invokes on a Valheim server.
const vhController = "/usr/games/serverconfig/controller/controller.sh"

func (g valheim) InstallMod(_ context.Context, req ModRequest) ([]string, error) {
	return []string{vhController + " install-mod " + remote.Quote(req.Ref)}, nil
}

func (g valheim) DeleteMod(req ModRequest) ([]string, error) {
	return []string{vhController + " delete-mod " + remote.Quote(req.Name)}, nil
}

func (valheim) DeleteFailure(req ModRequest, stdout string) error {
	if strings.Contains(stdout, "NOT_RUNNING") {
		return usererr.New("Start the server first - removing a mod's files needs its container running")
	}
	if strings.Contains(stdout, "NO_MATCH") {
		return usererr.Newf("Couldn't find a mod matching %q to delete", req.Name)
	}
	return nil
}

func (g valheim) Restart() ([]string, error) {
	return []string{vhController + " restart"}, nil
}

func (valheim) Update() ([]string, error) {
	return nil, ErrUnsupported
}

func (g valheim) ArchiveWorld(req ArchiveRequest) ([]string, error) {
	return []string{vhController + " archive-world " + remote.Quote(req.IncludePaths) + " " +
		remote.Quote(req.Bucket) + " " + remote.Quote(req.Key)}, nil
}

// Valheim's world settings live among its compose environment rather than in a config of their own,
// which is not something the control panel writes.
func (valheim) WorldDefaults() (WorldDefaults, error) {
	return WorldDefaults{}, ErrUnsupported
}

func (valheim) NewWorld(NewWorldRequest) (StoredWorld, error) {
	return StoredWorld{}, ErrUnsupported
}

func (valheim) SetPlayerRole(string, string) ([]string, error) {
	return nil, ErrUnsupported
}
