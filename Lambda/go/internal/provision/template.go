// Package provision creates and removes game servers.
package provision

import (
	"encoding/json"
	"fmt"
	"sort"
)

// Template is one game as the control panel can create it, resolved at deploy time: where its
// install script is published, what digest to verify it against, and what a server of it defaults
// to. The JSON names match what the CDK encodes into the Lambda's environment.
type Template struct {
	DisplayName            string  `json:"displayName"`
	InstallScriptURL       string  `json:"installScriptUrl"`
	InstallSha256          string  `json:"installSha256"`
	CompanionSha256        string  `json:"companionSha256"`
	DataPath               string  `json:"dataPath"`
	WorldDownloadPaths     string  `json:"worldDownloadPaths"`
	WorldDownloadCorePaths string  `json:"worldDownloadCorePaths"`
	SecurityGroupID        string  `json:"securityGroupId"`
	DefaultInstanceType    string  `json:"defaultInstanceType"`
	DefaultVolumeSizeGib   float64 `json:"defaultVolumeSizeGib"`
	RequiresGameVersion    bool    `json:"requiresGameVersion"`
}

// Templates is every game a server can be created from, by the name its servers are tagged with.
type Templates map[string]Template

func ParseTemplates(encoded string) (Templates, error) {
	if encoded == "" {
		return Templates{}, nil
	}
	var templates Templates
	if err := json.Unmarshal([]byte(encoded), &templates); err != nil {
		return nil, fmt.Errorf("parsing the game templates: %w", err)
	}
	return templates, nil
}

// Games names every game that can be created, in a stable order for showing to the caller.
func (t Templates) Games() []string {
	games := make([]string, 0, len(t))
	for game := range t {
		games = append(games, game)
	}
	sort.Strings(games)
	return games
}

// Option is a game as the new-server form offers it. Only the fields the form needs: the rest of a
// template says how to install the game, which is no business of the browser's.
type Option struct {
	Game                 string  `json:"game"`
	DisplayName          string  `json:"displayName"`
	RequiresGameVersion  bool    `json:"requiresGameVersion"`
	DefaultInstanceType  string  `json:"defaultInstanceType"`
	DefaultVolumeSizeGib float64 `json:"defaultVolumeSizeGib"`
}

func (t Templates) Options() []Option {
	options := make([]Option, 0, len(t))
	for _, game := range t.Games() {
		template := t[game]
		options = append(options, Option{
			Game:                 game,
			DisplayName:          template.DisplayName,
			RequiresGameVersion:  template.RequiresGameVersion,
			DefaultInstanceType:  template.DefaultInstanceType,
			DefaultVolumeSizeGib: template.DefaultVolumeSizeGib,
		})
	}
	return options
}
