package games

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"personal-game-server-manager/lambda/internal/usererr"
)

// modDBAPI is Vintage Story's mod registry.
const modDBAPI = "https://mods.vintagestory.at/api/mod/"

// modRegistry checks that the mod has a release for this game version. The game's own moddb client
// installs nothing and reports nothing when no release lists the version it is given, so the reason
// is looked up here instead.
//
// A registry that can't be reached is not an answer either way, so the install goes ahead.
func modRegistry(ctx context.Context, ref, gameVersion string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, modDBAPI+url.PathEscape(ref), nil)
	if err != nil {
		return nil
	}

	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		log.Printf("couldn't check mod %s against the registry: %v", ref, err)
		return nil
	}
	defer response.Body.Close()

	var body struct {
		Mod struct {
			Releases []struct {
				ModVersion string   `json:"modversion"`
				Tags       []string `json:"tags"`
			} `json:"releases"`
		} `json:"mod"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		log.Printf("couldn't read the registry's answer for mod %s: %v", ref, err)
		return nil
	}

	releases := body.Mod.Releases
	if len(releases) == 0 {
		return usererr.Newf("No mod called %s in the Vintage Story mod registry", ref)
	}
	for _, release := range releases {
		for _, tag := range release.Tags {
			if tag == gameVersion {
				return nil
			}
		}
	}

	newest := releases[0]
	return usererr.Newf("No release of %s lists game version %s. Its newest release (%s) lists %s - "+
		"install it by naming one of those as the game version",
		ref, gameVersion, newest.ModVersion, strings.Join(newest.Tags, ", "))
}

// pythonString renders a value as a Python string literal, for the scripts below that are generated
// here and run by python3 on the server.
func pythonString(s string) string {
	return strconv.Quote(s)
}
