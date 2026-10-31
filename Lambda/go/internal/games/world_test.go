package games

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNewWorldDropsEverythingNotAllowed(t *testing.T) {
	g, _ := For("vintagestory")
	body := `{"AllowPvP":false,"WelcomeMessage":"hi",
	  "Password":"stolen","Port":1234,"Ip":"0.0.0.0","Upnp":true,"MaxClients":99,
	  "WorldConfig":{"PlayStyle":"exploration","WorldConfiguration":{"startingClimate":"hot"}}}`
	w, err := g.NewWorld(NewWorldRequest{Name: "Frost Realm", Body: body})
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"Password", "Port", "Ip", "Upnp", "MaxClients"} {
		if _, found := w.Config[banned]; found {
			t.Errorf("%s survived at the top level", banned)
		}
	}
	if w.Config["AllowPvP"] != false || w.Config["WelcomeMessage"] != "hi" {
		t.Errorf("allowed settings did not survive: %v", w.Config)
	}
	encoded, _ := json.Marshal(w)
	if strings.Contains(string(encoded), "stolen") {
		t.Error("a password reached what gets stored")
	}
}

// WorldConfiguration is named as a whole, so a world option the panel has never heard of still
// reaches the game.
func TestEverythingInsideWorldConfigurationIsKept(t *testing.T) {
	g, _ := For("vintagestory")
	body := `{"WorldConfig":{"PlayStyle":"exploration",
	  "WorldConfiguration":{"startingClimate":"hot","alsoBrandNew":"yes"}}}`
	w, err := g.NewWorld(NewWorldRequest{Name: "Deep", Body: body})
	if err != nil {
		t.Fatal(err)
	}
	world := w.Config["WorldConfig"].(map[string]any)
	inner := world["WorldConfiguration"].(map[string]any)
	if inner["startingClimate"] != "hot" || inner["alsoBrandNew"] != "yes" {
		t.Errorf("WorldConfiguration was not kept whole: %v", inner)
	}
}

// WorldConfig is not named as a whole, so a setting beside WorldConfiguration has to be listed.
func TestAnUnlistedWorldConfigSettingIsDropped(t *testing.T) {
	g, _ := For("vintagestory")
	w, err := g.NewWorld(NewWorldRequest{Name: "Deep",
		Body: `{"WorldConfig":{"SaveFileLocation":"../../etc/passwd","NotListed":7}}`})
	if err != nil {
		t.Fatal(err)
	}
	world := w.Config["WorldConfig"].(map[string]any)
	for _, dropped := range []string{"SaveFileLocation", "NotListed"} {
		if _, found := world[dropped]; found {
			t.Errorf("%s was kept: %v", dropped, world[dropped])
		}
	}
}

// The two the form owns, whatever the block says about them.
func TestTheFormOwnsNameAndSeed(t *testing.T) {
	g, _ := For("vintagestory")
	body := `{"WorldConfig":{"WorldName":"ignored","Seed":"ignored"}}`
	w, err := g.NewWorld(NewWorldRequest{Name: "Frost Realm", Body: body, Seed: "12345"})
	if err != nil {
		t.Fatal(err)
	}
	world := w.Config["WorldConfig"].(map[string]any)
	if world["WorldName"] != "Frost Realm" || world["Seed"] != "12345" {
		t.Errorf("the form should win, got %v", world)
	}
}

func TestAnEmptySeedIsLeftNull(t *testing.T) {
	g, _ := For("vintagestory")
	w, err := g.NewWorld(NewWorldRequest{Name: "Fresh", Body: `{"WorldConfig":{"Seed":"old-world"}}`})
	if err != nil {
		t.Fatal(err)
	}
	world := w.Config["WorldConfig"].(map[string]any)
	if world["Seed"] != nil {
		t.Errorf("an empty seed should clear the last one, got %v", world["Seed"])
	}
}

func TestSeedsThatCannotBeWritten(t *testing.T) {
	g, _ := For("vintagestory")
	for _, seed := range []string{"a\nb", `"quoted"`, strings.Repeat("x", 65)} {
		if _, err := g.NewWorld(NewWorldRequest{Name: "ok", Body: `{}`, Seed: seed}); err == nil {
			t.Errorf("%q was accepted as a seed", seed)
		}
	}
}

func TestWorldNamesThatCannotNameAFile(t *testing.T) {
	g, _ := For("vintagestory")
	for _, name := range []string{"", "   ", "../..", "!!!", strings.Repeat("x", 49)} {
		if _, err := g.NewWorld(NewWorldRequest{Name: name, Body: `{}`}); err == nil {
			t.Errorf("%q was accepted as a world name", name)
		}
	}
}

func TestSlugsCannotEscapeTheSavesDirectory(t *testing.T) {
	g, _ := For("vintagestory")
	w, err := g.NewWorld(NewWorldRequest{Name: "my world", Body: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(w.Slug, "/.\\") {
		t.Errorf("slug %q could reach another directory", w.Slug)
	}
}

func TestBadJSONIsRefused(t *testing.T) {
	g, _ := For("vintagestory")
	if _, err := g.NewWorld(NewWorldRequest{Name: "ok", Body: `{nope`}); err == nil {
		t.Error("invalid JSON was accepted")
	}
}

func TestValheimHasNoWorldsYet(t *testing.T) {
	g, _ := For("valheim")
	if _, err := g.WorldDefaults(); err != ErrUnsupported {
		t.Errorf("want ErrUnsupported, got %v", err)
	}
}

// Every preset must parse and survive the allowlist, or picking it in the panel would fail only
// once someone tried it.
func TestEveryShippedPresetIsUsable(t *testing.T) {
	g, _ := For("vintagestory")
	defaults, err := g.WorldDefaults()
	if err != nil {
		t.Fatal(err)
	}
	if len(defaults.Presets) < 2 {
		t.Fatalf("expected the shipped presets, got %d", len(defaults.Presets))
	}
	for _, preset := range defaults.Presets {
		if preset.Name == "" || strings.Contains(preset.Name, ".json") {
			t.Errorf("preset name %q should be the file's stem", preset.Name)
		}
		w, err := g.NewWorld(NewWorldRequest{Name: "Probe", Body: preset.Body})
		if err != nil {
			t.Errorf("preset %q: %v", preset.Name, err)
			continue
		}
		if _, found := w.Config["WorldConfig"]; !found {
			t.Errorf("preset %q produced no WorldConfig: %v", preset.Name, w.Config)
		}
	}
}

// A preset says only what it changes; the instance merges it over what the server already has.
func TestAPartialPresetStaysPartial(t *testing.T) {
	g, _ := For("vintagestory")
	defaults, _ := g.WorldDefaults()
	var terra WorldPreset
	for _, p := range defaults.Presets {
		if p.Name == "terrapretty" {
			terra = p
		}
	}
	if terra.Body == "" {
		t.Skip("no terrapretty preset to check")
	}
	w, err := g.NewWorld(NewWorldRequest{Name: "Probe", Body: terra.Body})
	if err != nil {
		t.Fatal(err)
	}
	if _, found := w.Config["AllowPvP"]; found {
		t.Error("a partial preset gained settings it does not name")
	}
	world := w.Config["WorldConfig"].(map[string]any)
	inner := world["WorldConfiguration"].(map[string]any)
	if len(inner) > 10 {
		t.Errorf("a partial preset gained world options it does not name: %d", len(inner))
	}
}
