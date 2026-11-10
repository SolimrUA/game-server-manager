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
	{Path: "installmod", Command: "installMod", Params: []string{"instanceid", "modref", "modgameversion"}},
	{Path: "deletemod", Command: "deleteMod", Params: []string{"instanceid", "modname"}},
	{Path: "downloadworldstart", Command: "startWorldDownload", Params: []string{"instanceid", "scope"}},
	{Path: "downloadworldstatus", Command: "getWorldDownloadStatus", Params: []string{"instanceid", "commandid", "s3key"}},
	{Path: "serveroptions", Command: "getServerOptions"},
	{Path: "createserver", Command: "createServer",
		Params: []string{"game", "serverid", "instancetype", "gameversion", "volumesize", "snapshotid"}},
	{Path: "deleteserver", Command: "deleteServer", Params: []string{"instanceid"}},
	{Path: "gamepassword", Command: "getGamePassword", Params: []string{"instanceid"}},
	// POST keeps the password out of access logs and browser history.
	{Path: "setgamepassword", Command: "setGamePassword", Params: []string{"instanceid"}, Post: true},
	// POST because a name is free text, which does not belong in a URL.
	{Path: "setservername", Command: "setServerName", Params: []string{"instanceid"}, Post: true},
	{Path: "worlddefaults", Command: "getWorldDefaults", Params: []string{"instanceid"}},
	// POST because a settings block is free text of any length.
	{Path: "newworld", Command: "newWorld", Params: []string{"instanceid"}, Post: true},
	// POST because a player name is free text.
	{Path: "playerrole", Command: "setPlayerRole", Params: []string{"instanceid"}, Post: true},
	{Path: "access", Command: "getAccess"},
	{Path: "saveaccess", Command: "saveAccess", Post: true},
	{Path: "worlds", Command: "getWorlds"},
	{Path: "setbackupexpiry", Command: "setBackupExpiry", Params: []string{"snapshotid"}, Post: true},
	{Path: "setbackupprimary", Command: "setBackupPrimary", Params: []string{"snapshotid"}, Post: true},
	{Path: "usevolume", Command: "useVolume", Params: []string{"volumeid"}, Post: true},
	{Path: "retirevolume", Command: "retireVolume", Params: []string{"volumeid"}, Post: true},
	{Path: "deletevolume", Command: "deleteVolume", Params: []string{"volumeid"}, Post: true},
	{Path: "deletevolumebackups", Command: "deleteVolumeBackups", Params: []string{"volumeid"}, Post: true},
	{Path: "deletebackup", Command: "deleteBackup", Params: []string{"snapshotid"}, Post: true},
}

// paramNames maps a query-string parameter to the field the Lambda reads it as.
var paramNames = map[string]string{
	"instanceid":     "instanceId",
	"resize":         "reSizeType",
	"modref":         "modref",
	"modgameversion": "modGameVersion",
	"modname":        "modname",
	"scope":          "scope",
	"commandid":      "commandId",
	"s3key":          "s3Key",

	"game":         "game",
	"serverid":     "serverId",
	"instancetype": "instanceType",
	"gameversion":  "gameVersion",
	"volumesize":   "volumeSize",
	"snapshotid":   "snapshotId",
	"volumeid":     "volumeId",
}

// RequestTemplate builds the mapping template that turns a request into the Lambda's event. The
// Lambda verifies the caller's token itself, so identity does not depend on what reaches it in the
// payload.
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
	lines = append(lines, `"token": "$input.params('Authorization')"`)
	if e.Post {
		// Unquoted on purpose: $input.json emits JSON, so quoting it would nest a string.
		lines = append(lines, `"body": $input.json('$')`)
	}
	return "{\n " + strings.Join(lines, ",\n ") + "\n}"
}
