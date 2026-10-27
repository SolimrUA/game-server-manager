// Package games holds what differs between the games the control panel manages: the console
// commands and file edits that install a mod, remove one, restart a server or update it.
//
// Everything else about a server - how it is created, backed up, named or shut down - is the same
// whatever it runs, and lives outside this package.
package games

import (
	"context"
	"errors"

	"personal-game-server-manager/lambda/internal/usererr"
)

// Game is one game's way of being administered.
//
// Each method returns the shell commands to run on the server. A game that doesn't offer an
// operation returns ErrUnsupported.
type Game interface {
	// InstallMod installs a mod by the reference the mod registry knows it as.
	InstallMod(ctx context.Context, req ModRequest) ([]string, error)
	// DeleteMod removes a mod by the display name the control panel is showing.
	DeleteMod(req ModRequest) ([]string, error)
	// Restart stops and starts the game, leaving the instance running.
	Restart(dataPath string) ([]string, error)
	// Update puts the newest release of the game on the server and restarts it.
	Update(dataPath string) ([]string, error)
	// ArchiveWorld zips the world up and copies it to S3 for the caller to download.
	ArchiveWorld(req ArchiveRequest) ([]string, error)
	// DeleteFailure turns a failed DeleteMod into a message, since what "couldn't find it" looks like
	// is particular to how each game's mods are stored.
	DeleteFailure(req ModRequest, stdout string) error
}

// ErrUnsupported reports an operation a game doesn't offer.
var ErrUnsupported = errors.New("not supported for this game")

// ModRequest names a mod on a server.
type ModRequest struct {
	// Ref is the registry's own identifier for the mod.
	Ref string
	// Name is the mod's display name, which is all the control panel has when removing one.
	Name string
	// Version is the game version the mod has to match.
	Version  string
	DataPath string
}

// Scope says how much of a server's data to archive.
type Scope string

const (
	// Core is the world save alone.
	Core Scope = "core"
	// Full is everything the server keeps, short of its secrets.
	Full Scope = "full"
)

// ParseScope reads a requested scope, defaulting to Full for anything it doesn't recognise.
func ParseScope(s string) Scope {
	if Scope(s) == Core {
		return Core
	}
	return Full
}

// ArchiveRequest says what to archive and where to put it.
type ArchiveRequest struct {
	DataPath string
	// IncludePaths is the comma-separated list of folders this scope covers, from the server's own
	// tags. Never the whole data directory: that holds the join password.
	IncludePaths string
	Scope        Scope
	Bucket       string
	Key          string
}

// games is every game the control panel can administer, by the name its servers are tagged with.
var games = map[string]Game{
	"vintagestory": vintageStory{},
	"valheim":      valheim{},
}

// For returns how to administer a game, or an error naming it if the control panel doesn't know it.
func For(name string) (Game, error) {
	game, ok := games[name]
	if !ok {
		return nil, usererr.Newf("%q isn't a game this control panel knows how to administer", name)
	}
	return game, nil
}
