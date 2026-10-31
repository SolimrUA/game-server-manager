package games

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"personal-game-server-manager/lambda/internal/usererr"
)

// WorldDefaults is what the control panel offers to start a new world from. The format travels with
// it because what a reader has to parse differs per game.
type WorldDefaults struct {
	Format  string        `json:"format"`
	Presets []WorldPreset `json:"presets"`
}

// WorldPreset is one set of settings to generate a world from. A preset says only what it changes:
// the instance merges it over the config the server already has, so anything left out stays as it is.
type WorldPreset struct {
	Name string `json:"name"`
	Body string `json:"body"`
}

// NewWorldRequest is a world asked for through the control panel.
type NewWorldRequest struct {
	// Name is what the player calls the world. The save file is named after it rather than by it:
	// the path is derived on the instance, so nothing typed here reaches a filename unchanged.
	Name string
	// Body is the settings block as edited, in the game's config format.
	Body string
	// Seed is the world's seed. Empty leaves the game to pick one.
	Seed string
}

// StoredWorld is what gets recorded for a server, and what its instance reads back before the game
// starts.
type StoredWorld struct {
	// Slug names the save file the game generates.
	Slug string `json:"slug"`
	// Name is what the world is called in-game.
	Name string `json:"name"`
	// Config holds the settings this game lets a new world set, shaped as the game's own config is
	// so the instance can merge it in whole.
	Config map[string]any `json:"config"`
}

// worldNamePattern keeps a name to what can be shown and slugged. The slug, not the name, reaches
// the filesystem.
var worldNamePattern = regexp.MustCompile(`^[\p{L}\p{N} '_-]{1,48}$`)

// slugPattern is what a derived save-file name is allowed to be, checked rather than assumed so a
// name that slugs to nothing can't produce a path of its own.
var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)

var nonSlugRunes = regexp.MustCompile(`[^a-z0-9]+`)

func worldSlug(name string) (string, error) {
	slug := strings.Trim(nonSlugRunes.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if !slugPattern.MatchString(slug) {
		return "", usererr.New("That world name has no letters or digits to name a save file after")
	}
	return slug, nil
}

// checkWorldName reports whether a name is one a world can be called.
func checkWorldName(name string) error {
	if !worldNamePattern.MatchString(name) {
		return usererr.New("A world name is 1 to 48 letters, digits, spaces, apostrophes, " +
			"hyphens or underscores")
	}
	return nil
}

// worldSeedPattern is what a seed may be. It is written into the game's config rather than a
// command, so this only keeps out what would end a line or a JSON string early.
var worldSeedPattern = regexp.MustCompile(`^[\p{L}\p{N} ._-]{0,64}$`)

func checkWorldSeed(seed string) error {
	if !worldSeedPattern.MatchString(seed) {
		return usererr.New("A seed is up to 64 letters, digits, spaces, dots, hyphens or underscores")
	}
	return nil
}

func parseWorldBody(body string) (map[string]any, error) {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return nil, usererr.New(fmt.Sprintf("Those world settings aren't valid JSON: %v", err))
	}
	return parsed, nil
}
