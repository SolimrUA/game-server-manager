// Package control answers the control panel's requests: it says what each server is doing and acts
// on the ones the caller is allowed to act on.
package control

import (
	"context"
	"fmt"
	"log"

	"personal-game-server-manager/lambda/internal/access"
	"personal-game-server-manager/lambda/internal/server"
	"personal-game-server-manager/lambda/internal/usererr"
)

// Request is one call from the control panel, as the API's mapping template builds it. Absent query
// parameters arrive as empty strings rather than missing fields, so every one of them is a string.
type Request struct {
	Command string `json:"command"`

	// TagName and TagValue select the instances this control panel manages.
	TagName  string `json:"tagName"`
	TagValue string `json:"tagValue"`

	// Groups and Sub come from the Cognito authorizer and say who is asking.
	Groups string `json:"groups"`
	Sub    string `json:"sub"`

	// InstanceID restricts the action to one server instead of every one the caller can see.
	InstanceID string `json:"instanceId"`

	ReSizeType     string `json:"reSizeType"`
	ModRef         string `json:"modref"`
	ModName        string `json:"modname"`
	ModGameVersion string `json:"modGameVersion"`
	Scope          string `json:"scope"`
	CommandID      string `json:"commandId"`
	S3Key          string `json:"s3Key"`

	Game         string `json:"game"`
	ServerID     string `json:"serverId"`
	InstanceType string `json:"instanceType"`
	GameVersion  string `json:"gameVersion"`
	VolumeSize   string `json:"volumeSize"`
	SnapshotID   string `json:"snapshotId"`

	Body struct {
		Changes []access.Change `json:"changes"`
		Names   []access.Name   `json:"names"`
	} `json:"body"`
}

// Info is the state of every server the caller may see, which accompanies every answer so the
// control panel can redraw itself from any response.
type Info struct {
	Instances []server.Server `json:"Instances"`
}

// caller is who is asking, and what they may do.
type caller struct {
	sub     string
	isAdmin bool
	// roles is the access template each server grants this caller, by server id. Empty for an admin,
	// who holds everything on every server.
	roles map[string]string
}

// run carries out a command. servers is what the caller may act on: the one they named, or all of
// theirs when they named none.
type run func(ctx context.Context, h *Handler, req Request, servers []server.Server) (any, error)

// command is one thing the control panel can ask for, and who may ask for it.
//
// Exactly one of adminOnly, needs or openToAll has to be set. The table is checked at startup, so a
// command added without saying who may run it stops the function from starting rather than
// defaulting to something permissive.
type command struct {
	// adminOnly is for commands that manage the fleet itself rather than one server, which no access
	// template can grant.
	adminOnly bool
	// needs is the capability a caller must hold on the server they named.
	needs access.Capability
	// openToAll is for commands that reveal nothing beyond what the caller can already see.
	openToAll bool
	// fleetWide marks a command that works when there are no servers at all - which is how the first
	// one gets created.
	fleetWide bool

	run run
}

// commands is the whole control API: what can be asked for, who may ask, and what it does.
var commands = map[string]command{
	"getInfo": {openToAll: true, fleetWide: true, run: getInfo},

	"start":  {needs: access.Lifecycle, run: start},
	"stop":   {needs: access.Lifecycle, run: stop},
	"reSize": {needs: access.Resize, run: resize},

	"backupNow": {needs: access.Backup, run: backupNow},

	"pauseIdleShutdown":  {needs: access.AutoShutdown, run: pauseIdleShutdown},
	"resumeIdleShutdown": {needs: access.AutoShutdown, run: resumeIdleShutdown},

	"installMod":  {needs: access.Content, run: installMod},
	"deleteMod":   {needs: access.Content, run: deleteMod},
	"restartGame": {needs: access.Content, run: restartGame},
	"updateGame":  {needs: access.Content, run: updateGame},

	"startWorldDownload":     {needs: access.Download, run: startWorldDownload},
	"getWorldDownloadStatus": {needs: access.Download, run: worldDownloadStatus},

	"getServerOptions": {adminOnly: true, fleetWide: true, run: serverOptions},
	"createServer":     {adminOnly: true, fleetWide: true, run: createServer},
	"deleteServer":     {adminOnly: true, run: deleteServer},

	"getAccess":  {adminOnly: true, fleetWide: true, run: getAccess},
	"saveAccess": {adminOnly: true, fleetWide: true, run: saveAccess},
}

func init() {
	for name, c := range commands {
		set := 0
		for _, declared := range []bool{c.adminOnly, c.needs != "", c.openToAll} {
			if declared {
				set++
			}
		}
		if set != 1 {
			panic(fmt.Sprintf("command %q must declare exactly one of adminOnly, needs or openToAll", name))
		}
		if c.run == nil {
			panic(fmt.Sprintf("command %q has nothing to run", name))
		}
	}
}

// Handle answers one request.
//
// Every answer carries the state of the caller's servers alongside it, so a failure still leaves the
// control panel with something current to show. Only a few commands answer with something else
// entirely, and those return it themselves.
func (h *Handler) Handle(ctx context.Context, req Request) (any, error) {
	c, known := commands[req.Command]
	if !known {
		return nil, fmt.Errorf("unknown command %q", req.Command)
	}

	who, err := h.who(ctx, req)
	if err != nil {
		return nil, err
	}

	servers, err := h.visible(ctx, req, who)
	if err != nil {
		return nil, err
	}
	info := Info{Instances: servers}

	if len(servers) == 0 && !c.fleetWide {
		// With access filtering, "nothing to show" is an ordinary state for a caller who hasn't been
		// granted anything yet, not an error.
		return reply("No gaming server instances found", info), nil
	}

	targets, err := target(req, servers)
	if err != nil {
		return reply(err.Error(), info), nil
	}

	if err := authorize(c, who, targets); err != nil {
		return reply(err.Error(), info), nil
	}

	answer, err := c.run(ctx, h, req, targets)
	if err != nil {
		if message, ok := usererr.Message(err); ok {
			return reply(message, info), nil
		}
		log.Printf("%s failed: %v", req.Command, err)
		return reply(failureMessage(req.Command), info), nil
	}

	// A command that answered with something of its own, such as a download handle, is returned as it
	// is; anything else is a message to show with the refreshed state.
	if message, ok := answer.(string); ok {
		return reply(message, info), nil
	}
	return answer, nil
}

// reply is the two-element answer the control panel reads: a message, then the current state.
func reply(message string, info Info) []any {
	return []any{message, info}
}

func (h *Handler) who(ctx context.Context, req Request) (caller, error) {
	who := caller{sub: req.Sub, isAdmin: access.IsAdmin(access.Groups(req.Groups))}
	if who.isAdmin {
		return who, nil
	}

	roles, err := h.grants.RolesFor(ctx, req.Sub)
	if err != nil {
		// Reading no grants is indistinguishable from holding none, and holding none is safe.
		log.Printf("couldn't read access for %s: %v", req.Sub, err)
	}
	who.roles = roles
	return who, nil
}

// target narrows the caller's servers to the one they named. A server missing from their own list is
// reported the same way whether it doesn't exist or isn't theirs.
func target(req Request, servers []server.Server) ([]server.Server, error) {
	if req.InstanceID == "" {
		return servers, nil
	}
	for _, s := range servers {
		if s.InstanceID == req.InstanceID {
			return []server.Server{s}, nil
		}
	}
	return nil, usererr.New("Requested instanceId was not found among your gaming server instances")
}

func authorize(c command, who caller, targets []server.Server) error {
	if who.isAdmin || c.openToAll {
		return nil
	}
	if c.adminOnly {
		return usererr.New("You don't have permission to manage servers or access")
	}

	// The capability is read from the server's own entry, which was built from the caller's grants -
	// so a server they can't see can't be acted on whatever they asked for it.
	for _, s := range targets {
		if !access.For(false, who.roles[s.ServerID]).Has(c.needs) {
			return usererr.New("You don't have permission to perform this action on this server")
		}
	}
	if len(targets) == 0 {
		return usererr.New("You don't have permission to perform this action")
	}
	return nil
}

// failureMessage is what the caller is told when a command failed for a reason that is ours rather
// than theirs.
func failureMessage(command string) string {
	if message, ok := failureMessages[command]; ok {
		return message
	}
	return "Something went wrong, please try again later"
}

var failureMessages = map[string]string{
	"start":              "Couldn't start server, please try again later",
	"stop":               "Stopping server failed - please wait a few minutes and try again",
	"reSize":             "Something went wrong with resizing your server, please try again later",
	"backupNow":          "Couldn't start backup, please try again later",
	"pauseIdleShutdown":  "Couldn't pause auto-shutdown, please try again later",
	"resumeIdleShutdown": "Couldn't resume auto-shutdown, please try again later",
	"installMod":         "Couldn't install mod, please try again later",
	"deleteMod":          "Couldn't delete mod, please try again later",
	"restartGame":        "Couldn't restart the game, please try again later",
	"updateGame":         "Couldn't update the game, please try again later",
	"createServer":       "Couldn't create the server, please try again later",
	"deleteServer":       "Couldn't delete the server, please try again later",
	"saveAccess":         "Couldn't save access, please try again later",
}
