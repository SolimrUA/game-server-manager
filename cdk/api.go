package main

import (
	"fmt"
	"strings"
)

// Endpoint is one path on the control API. Every one of them invokes the same Lambda and is told which
// command to run by its request template, so the whole API is this table plus the loop in addApi.
type Endpoint struct {
	Path    string
	Command string
	// Query-string parameters the request template forwards, beyond tagname/tagvalue which every
	// endpoint takes. The Lambda reads them under these names.
	Params []string
	// Post sends the request body through instead of a query string, for payloads too big for a URL.
	Post bool
}

func (e Endpoint) Method() string {
	if e.Post {
		return "POST"
	}
	return "GET"
}

// Endpoints is the control API.
var Endpoints = []Endpoint{
	{Path: "getinfo", Command: "getInfo"},
	{Path: "start", Command: "start", Params: []string{"instanceid"}},
	{Path: "stop", Command: "stop", Params: []string{"instanceid"}},
	{Path: "resize", Command: "reSize", Params: []string{"resize", "instanceid"}},
	{Path: "backupnow", Command: "backupNow", Params: []string{"instanceid"}},
	{Path: "pauseidleshutdown", Command: "pauseIdleShutdown", Params: []string{"instanceid"}},
	{Path: "resumeidleshutdown", Command: "resumeIdleShutdown", Params: []string{"instanceid"}},
	{Path: "restartgame", Command: "restartGame", Params: []string{"instanceid"}},
	{Path: "updategame", Command: "updateGame", Params: []string{"instanceid"}},
	{Path: "installmod", Command: "installMod", Params: []string{"instanceid", "modref"}},
	{Path: "deletemod", Command: "deleteMod", Params: []string{"instanceid", "modname"}},
	{Path: "downloadworldstart", Command: "startWorldDownload", Params: []string{"instanceid", "scope"}},
	{Path: "downloadworldstatus", Command: "getWorldDownloadStatus", Params: []string{"instanceid", "commandid", "s3key"}},
	{Path: "access", Command: "getAccess"},
	{Path: "saveaccess", Command: "saveAccess", Post: true},
}

// paramNames maps a query-string parameter to the field the Lambda reads it as.
var paramNames = map[string]string{
	"instanceid": "instanceId",
	"resize":     "reSizeType",
	"modref":     "modref",
	"modname":    "modname",
	"scope":      "scope",
	"commandid":  "commandId",
	"s3key":      "s3Key",
}

// RequestTemplate builds the mapping template that turns a request into the Lambda's event. The groups
// and sub claims come from the Cognito authorizer; the Lambda needs sub to resolve per-server access.
//
// API Gateway renders the multi-valued cognito:groups claim as the string "[admins, users]" rather than
// JSON, which is why the Lambda strips brackets instead of parsing it.
func (e Endpoint) RequestTemplate() string {
	lines := []string{
		fmt.Sprintf(`"command": "%s"`, e.Command),
		`"tagName": "$input.params('tagname')"`,
		`"tagValue": "$input.params('tagvalue')"`,
	}
	for _, p := range e.Params {
		field, ok := paramNames[p]
		if !ok {
			panic("no Lambda field mapped for query parameter " + p)
		}
		lines = append(lines, fmt.Sprintf(`"%s": "$input.params('%s')"`, field, p))
	}
	lines = append(lines,
		`"groups": "$context.authorizer.claims.get('cognito:groups')"`,
		`"sub": "$context.authorizer.claims.sub"`,
	)
	if e.Post {
		// Unquoted on purpose: $input.json emits JSON, so quoting it would nest a string.
		lines = append(lines, `"body": $input.json('$')`)
	}
	return "{\n " + strings.Join(lines, ",\n ") + "\n}"
}
