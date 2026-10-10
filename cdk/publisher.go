package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// lookupStackOutputs reads a deployed stack's outputs at synth time.
func lookupStackOutputs(stackName, region string) (map[string]string, error) {
	cmd := exec.Command("aws", "cloudformation", "describe-stacks",
		"--stack-name", stackName,
		"--region", region,
		"--query", "Stacks[0].Outputs",
		"--output", "json")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("could not read outputs of stack %q; deploy it first", stackName)
	}

	var entries []struct {
		OutputKey   string `json:"OutputKey"`
		OutputValue string `json:"OutputValue"`
	}
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, fmt.Errorf("could not read outputs of stack %q: %w", stackName, err)
	}

	values := map[string]string{}
	for _, e := range entries {
		values[e.OutputKey] = e.OutputValue
	}
	return values, nil
}

// ResolvedTemplate is a GameTemplate with the published script URLs, digests and security group filled
// in. The control Lambda creates servers from these alone, so it needs no lookups of its own.
type ResolvedTemplate struct {
	DisplayName            string  `json:"displayName"`
	InstallScriptUrl       string  `json:"installScriptUrl"`
	InstallSha256          string  `json:"installSha256"`
	CompanionSha256        string  `json:"companionSha256"`
	DataPath               string  `json:"dataPath"`
	WorldDownloadPaths     string  `json:"worldDownloadPaths"`
	WorldDownloadCorePaths string  `json:"worldDownloadCorePaths"`
	SecurityGroupId        string  `json:"securityGroupId"`
	DefaultInstanceType    string  `json:"defaultInstanceType"`
	DefaultVolumeSizeGib   float64 `json:"defaultVolumeSizeGib"`
	RequiresGameVersion    bool    `json:"requiresGameVersion"`
}

// resolveTemplates pairs each template with the asset publisher's current script digests and the
// security group the fleet stack made for that game.
func resolveTemplates(publisherOutputs map[string]string, securityGroupIds map[string]*string) (string, error) {
	base := strings.TrimRight(publisherOutputs["ScriptsBaseUrl"], "/")
	if base == "" {
		return "", fmt.Errorf("the asset publisher has no ScriptsBaseUrl output")
	}

	resolved := map[string]ResolvedTemplate{}
	for _, t := range GameTemplates {
		installSha := publisherOutputs[t.InstallShaOutput]
		if installSha == "" {
			return "", fmt.Errorf("the asset publisher has no %s output", t.InstallShaOutput)
		}
		groupId := securityGroupIds[t.Game]
		if groupId == nil {
			return "", fmt.Errorf("no security group for game %q", t.Game)
		}
		resolved[t.Game] = ResolvedTemplate{
			DisplayName:            t.DisplayName,
			InstallScriptUrl:       base + "/" + t.InstallScript,
			InstallSha256:          installSha,
			CompanionSha256:        publisherOutputs[t.CompanionShaOutput],
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
