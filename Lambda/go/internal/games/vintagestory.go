package games

import (
	"context"
	"embed"
	"regexp"
	"strings"

	"personal-game-server-manager/lambda/internal/remote"
	"personal-game-server-manager/lambda/internal/usererr"
)

type vintageStory struct{}

// vsController is the one entry point the control panel invokes on a Vintage Story server.
const vsController = "/home/vintagestory/controller/controller.sh"

// InstallMod requires the server's game version: without it the game's own moddb client looks
// "current" up itself, through a request that fails.
func (g vintageStory) InstallMod(ctx context.Context, req ModRequest) ([]string, error) {
	if req.Version == "" {
		return nil, usererr.New("Couldn't determine the server's current game version - " +
			"try again once the status agent has reported it")
	}
	if err := modRegistry(ctx, req.Ref, req.Version); err != nil {
		return nil, err
	}
	return []string{
		vsController + " install-mod " + remote.Quote(req.Ref) + " " + remote.Quote(req.Version),
	}, nil
}

func (g vintageStory) DeleteMod(req ModRequest) ([]string, error) {
	return []string{vsController + " delete-mod " + remote.Quote(req.Name)}, nil
}

func (vintageStory) DeleteFailure(req ModRequest, stdout string) error {
	if strings.Contains(stdout, "NO_MATCH") {
		return usererr.Newf("Couldn't find a mod matching %q to delete", req.Name)
	}
	return nil
}

func (g vintageStory) Restart() ([]string, error) {
	return []string{vsController + " restart"}, nil
}

func (g vintageStory) Update() ([]string, error) {
	return []string{vsController + " update"}, nil
}

func (g vintageStory) ArchiveWorld(req ArchiveRequest) ([]string, error) {
	return []string{vsController + " archive-world " + remote.Quote(string(req.Scope)) + " " +
		remote.Quote(req.IncludePaths) + " " + remote.Quote(req.Bucket) + " " +
		remote.Quote(req.Key)}, nil
}

//go:embed worlds/vintagestory
var vsWorldPresets embed.FS

// worldPresets reads every preset a game ships, named after its file. Adding one is adding a file:
// nothing here lists them.
func worldPresets(files embed.FS, dir string) ([]WorldPreset, error) {
	entries, err := files.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	presets := make([]WorldPreset, 0, len(entries))
	for _, entry := range entries {
		body, err := files.ReadFile(dir + "/" + entry.Name())
		if err != nil {
			return nil, err
		}
		presets = append(presets, WorldPreset{
			Name: strings.TrimSuffix(entry.Name(), ".json"),
			Body: string(body),
		})
	}
	return presets, nil
}

// vsWorldKeys are the settings a new world may be given, named by where they sit in
// serverconfig.json. A dot separates a nested setting from the section holding it, which Vintage
// Story can afford because none of its own keys contain one.
//
// WorldConfiguration is named as a whole, so every world option inside it reaches the game, down to
// the ones a later release adds. WorldConfig is not, so a setting beside it has to be named here.
var vsWorldKeys = []string{
	"ServerDescription",
	"WelcomeMessage",
	"MapSizeX",
	"MapSizeY",
	"MapSizeZ",
	"AllowPvP",
	"AllowFireSpread",
	"AllowFallingBlocks",
	"ModIdBlackList",
	"ModIdWhiteList",
	"WorldConfig.AllowCreativeMode",
	"WorldConfig.PlayStyle",
	"WorldConfig.PlayStyleLangCode",
	"WorldConfig.WorldType",
	"WorldConfig.WorldConfiguration",
	"WorldConfig.MapSizeY",
}

// vsAllowed keeps only the settings vsWorldKeys names, rebuilding them in the shape
// serverconfig.json has them. Everything else is dropped rather than rejected, so a block pasted
// whole from a server's own config still works.
func vsAllowed(parsed map[string]any) map[string]any {
	kept := map[string]any{}
	for _, key := range vsWorldKeys {
		path := strings.Split(key, ".")
		if value, found := vsLookup(parsed, path); found {
			vsPut(kept, path, value)
		}
	}
	return kept
}

func vsLookup(from map[string]any, path []string) (any, bool) {
	for _, step := range path[:len(path)-1] {
		nested, ok := from[step].(map[string]any)
		if !ok {
			return nil, false
		}
		from = nested
	}
	value, found := from[path[len(path)-1]]
	return value, found
}

func vsPut(into map[string]any, path []string, value any) {
	for _, step := range path[:len(path)-1] {
		nested, ok := into[step].(map[string]any)
		if !ok {
			nested = map[string]any{}
			into[step] = nested
		}
		into = nested
	}
	into[path[len(path)-1]] = value
}

func (vintageStory) WorldDefaults() (WorldDefaults, error) {
	presets, err := worldPresets(vsWorldPresets, "worlds/vintagestory")
	if err != nil {
		return WorldDefaults{}, err
	}
	return WorldDefaults{Format: "json", Presets: presets}, nil
}

func (vintageStory) NewWorld(req NewWorldRequest) (StoredWorld, error) {
	if err := checkWorldName(req.Name); err != nil {
		return StoredWorld{}, err
	}
	if err := checkWorldSeed(req.Seed); err != nil {
		return StoredWorld{}, err
	}
	slug, err := worldSlug(req.Name)
	if err != nil {
		return StoredWorld{}, err
	}
	parsed, err := parseWorldBody(req.Body)
	if err != nil {
		return StoredWorld{}, err
	}

	config := vsAllowed(parsed)
	vsPut(config, []string{"WorldConfig", "WorldName"}, req.Name)
	// Left null rather than unset, so a world gets a seed of its own instead of the one the server
	// was last generated with.
	var seed any
	if req.Seed != "" {
		seed = req.Seed
	}
	vsPut(config, []string{"WorldConfig", "Seed"}, seed)

	return StoredWorld{Slug: slug, Name: req.Name, Config: config}, nil
}

// playerNamePattern and rolePattern are what the console will take. Both end up in one console
// line, which the game splits on spaces itself, so they are held to characters that cannot be read
// as a further argument.
var playerNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

var rolePattern = regexp.MustCompile(`^[a-z0-9]{1,32}$`)

func (g vintageStory) SetPlayerRole(player, role string) ([]string, error) {
	if !playerNamePattern.MatchString(player) {
		return nil, usererr.New("A player name is 1 to 32 letters, digits, underscores or hyphens")
	}
	if !rolePattern.MatchString(role) {
		return nil, usererr.New("A role code is 1 to 32 lowercase letters or digits, as the server's " +
			"own roles are written")
	}
	return []string{
		vsController + " set-player-role " + remote.Quote(player) + " " + remote.Quote(role),
	}, nil
}
