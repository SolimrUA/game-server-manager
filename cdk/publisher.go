package main

import (
	"encoding/json"
	"fmt"
)

// ResolvedTemplate is a GameTemplate with the published script URLs, digests and security group filled
// in. The control Lambda creates servers from these alone, so it needs no lookups of its own.
type ResolvedTemplate struct {
	DisplayName            string  `json:"displayName"`
	InstallScriptUrl       string  `json:"installScriptUrl"`
	InstallSha256          string  `json:"installSha256"`
	CompanionSha256        string  `json:"companionSha256"`
	ApplyConfigSha256      string  `json:"applyConfigSha256"`
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
func resolveTemplates(config Config, securityGroupIds map[string]*string) (string, error) {
	base := scriptsBaseUrl(config)

	resolved := map[string]ResolvedTemplate{}
	for _, t := range GameTemplates {
		digests := map[string]string{}
		for _, script := range []string{t.InstallScript, t.CompanionScript, t.ApplyConfigScript} {
			digest, err := scriptDigest("../scripts/" + script)
			if err != nil {
				return "", fmt.Errorf("digesting %s: %w", script, err)
			}
			digests[script] = digest
		}
		groupId := securityGroupIds[t.Game]
		if groupId == nil {
			return "", fmt.Errorf("no security group for game %q", t.Game)
		}
		resolved[t.Game] = ResolvedTemplate{
			DisplayName:            t.DisplayName,
			InstallScriptUrl:       base + "/" + t.InstallScript,
			InstallSha256:          digests[t.InstallScript],
			CompanionSha256:        digests[t.CompanionScript],
			ApplyConfigSha256:      digests[t.ApplyConfigScript],
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
