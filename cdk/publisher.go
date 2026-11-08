package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolvedTemplate is a GameTemplate with the published script URLs, digests and security group filled
// in. The control Lambda creates servers from these alone, so it needs no lookups of its own.
type ResolvedTemplate struct {
	DisplayName            string  `json:"displayName"`
	InstallScriptUrl       string  `json:"installScriptUrl"`
	InstallSha256          string  `json:"installSha256"`
	DigestsSha256          string  `json:"digestsSha256"`
	DataPath               string  `json:"dataPath"`
	WorldDownloadPaths     string  `json:"worldDownloadPaths"`
	WorldDownloadCorePaths string  `json:"worldDownloadCorePaths"`
	SecurityGroupId        string  `json:"securityGroupId"`
	DefaultInstanceType    string  `json:"defaultInstanceType"`
	DefaultVolumeSizeGib   float64 `json:"defaultVolumeSizeGib"`
	RequiresGameVersion    bool    `json:"requiresGameVersion"`
}

// resolveTemplates pairs each template with the digests of the scripts this deploy publishes and the
// security group the fleet stack made for that game.
func resolveTemplates(config Config, securityGroupIds map[string]*string,
	digests map[string]string) (string, error) {
	base := scriptsBaseUrl(config)

	resolved := map[string]ResolvedTemplate{}
	for _, t := range GameTemplates {
		installDigest, err := scriptDigest("../scripts/" + t.InstallScript)
		if err != nil {
			return "", fmt.Errorf("digesting %s: %w", t.InstallScript, err)
		}
		groupId := securityGroupIds[t.Game]
		if groupId == nil {
			return "", fmt.Errorf("no security group for game %q", t.Game)
		}
		resolved[t.Game] = ResolvedTemplate{
			DisplayName:            t.DisplayName,
			InstallScriptUrl:       base + "/" + t.InstallScript,
			InstallSha256:          installDigest,
			DigestsSha256:          digests[t.Game],
			DataPath:               t.DataPath,
			WorldDownloadPaths:     t.WorldDownloadPaths,
			WorldDownloadCorePaths: t.WorldDownloadCorePaths,
			SecurityGroupId:        *groupId,
			DefaultInstanceType:    t.DefaultInstanceType,
			DefaultVolumeSizeGib:   t.DefaultVolumeSizeGib,
			RequiresGameVersion:    t.RequiresGameVersion,
		}
	}

	encoded, err := json.Marshal(resolved)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

const manifestName = "digests.txt"

// writeDigestManifests publishes each game's list of script digests and returns the digest of each
// list. That one digest is all a new instance is given: it pins the list, and the list pins every
// script, so the bucket the list is served from decides nothing about what may run on a server.
func writeDigestManifests(dir string) (map[string]string, error) {
	if err := os.RemoveAll(dir); err != nil {
		return nil, err
	}

	digests := map[string]string{}
	for _, t := range GameTemplates {
		manifest, err := digestManifest(t)
		if err != nil {
			return nil, err
		}
		//Published beside the install script, so an instance can fetch the list from the only URL it
		//is given.
		path := filepath.Join(dir, filepath.Dir(t.InstallScript), manifestName)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, []byte(manifest), 0o644); err != nil {
			return nil, err
		}
		digests[t.Game] = textDigest(manifest)
	}
	return digests, nil
}

func digestManifest(t GameTemplate) (string, error) {
	var lines strings.Builder
	for _, script := range t.Scripts {
		digest, err := scriptDigest("../scripts/" + script)
		if err != nil {
			return "", fmt.Errorf("digesting %s: %w", script, err)
		}
		fmt.Fprintf(&lines, "%s  %s\n", digest, script)
	}
	return lines.String(), nil
}
