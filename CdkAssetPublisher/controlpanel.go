package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ControlPanelOutputs holds the values FrontEnd/js/config.js needs baked into it, all of which the
// Control Panel stack only knows once it has created them.
type ControlPanelOutputs struct {
	ControlPanelBucketArn string
	// The host the control panel is served on, which is the custom domain when one is set. Cognito
	// accepts callback URLs only under that host.
	ControlPanelUrl string
	ApiUrl          string
	UserPoolId      string
	ClientId        string
	CognitoDomain   string
	TagName         string
	TagValue        string
}

// lookupControlPanelOutputs reads the Control Panel stack's outputs at synth time, so the files are
// staged locally with real values and can be inspected before anything is uploaded. Reports found=false
// when the stack can't be read, which is the normal case on a first install.
func lookupControlPanelOutputs(stackName, region string) (ControlPanelOutputs, bool, error) {
	cmd := exec.Command("aws", "cloudformation", "describe-stacks",
		"--stack-name", stackName,
		"--region", region,
		"--query", "Stacks[0].Outputs",
		"--output", "json")
	out, err := cmd.Output()
	if err != nil {
		return ControlPanelOutputs{}, false, nil
	}

	var entries []struct {
		OutputKey   string `json:"OutputKey"`
		OutputValue string `json:"OutputValue"`
	}
	if err := json.Unmarshal(out, &entries); err != nil {
		return ControlPanelOutputs{}, false, fmt.Errorf("could not read %s outputs: %w", stackName, err)
	}

	values := map[string]string{}
	for _, e := range entries {
		values[e.OutputKey] = e.OutputValue
	}

	outputs := ControlPanelOutputs{
		ControlPanelBucketArn: values["S3Bucket"],
		ControlPanelUrl:       values["ControlPanelUrl"],
		ApiUrl:                values["ControlApiUrl"],
		UserPoolId:            values["CognitoUserPoolId"],
		ClientId:              values["CognitoClientID"],
		CognitoDomain:         values["CognitoDomainName"],
		TagName:               values["TagName"],
		TagValue:              values["TagValue"],
	}

	var missing []string
	for key, value := range map[string]string{
		"S3Bucket": outputs.ControlPanelBucketArn, "ControlPanelUrl": outputs.ControlPanelUrl,
		"ControlApiUrl": outputs.ApiUrl, "CognitoUserPoolId": outputs.UserPoolId,
		"CognitoClientID": outputs.ClientId, "CognitoDomainName": outputs.CognitoDomain,
		"TagName": outputs.TagName, "TagValue": outputs.TagValue,
	} {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return ControlPanelOutputs{}, false, fmt.Errorf("%s is missing outputs %s", stackName, strings.Join(missing, ", "))
	}

	return outputs, true, nil
}

// stageControlPanel copies FrontEnd/ to its own build directory, outside the assets build directory so
// that it reaches the control panel's bucket only, and fills in config.js on the way.
// FrontEnd/js/config.js stays the one definition of that file's shape; this replaces its placeholders.
func stageControlPanel(destination, stackName string, outputs ControlPanelOutputs) error {
	if err := os.RemoveAll(destination); err != nil {
		return err
	}
	if err := copyDir("../FrontEnd", destination); err != nil {
		return err
	}
	// FrontEnd/README.md documents the directory for whoever reads the repo; it isn't part of the site.
	if err := os.Remove(filepath.Join(destination, "README.md")); err != nil && !os.IsNotExist(err) {
		return err
	}

	configPath := filepath.Join(destination, "js", "config.js")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}

	contents := string(data)
	for placeholder, value := range map[string]string{
		"REPLACE-WITH-CFURL":      outputs.ControlPanelUrl,
		"REPLACE-WITH-COGNITO":    outputs.ClientId,
		"REPLACE-WITH-COGDOMAIN":  outputs.CognitoDomain,
		"REPLACE-WITH-APIURL":     outputs.ApiUrl,
		"REPLACE-WITH-POOLS-ID":   outputs.UserPoolId,
		"REPLACE-WITH-IDTAGNAME":  outputs.TagName,
		"REPLACE-WITH-IDTAGVALUE": outputs.TagValue,
		"REPLACE-WITH-STACKNAME":  stackName,
	} {
		contents = strings.ReplaceAll(contents, placeholder, value)
	}

	if remaining := strings.Count(contents, "REPLACE-WITH-"); remaining > 0 {
		return fmt.Errorf("%s still has %d unfilled placeholder(s) after substitution", configPath, remaining)
	}

	return os.WriteFile(configPath, []byte(contents), 0o644)
}
