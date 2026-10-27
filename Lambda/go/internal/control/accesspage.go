package control

import (
	"context"
	"fmt"
	"log"

	"personal-game-server-manager/lambda/internal/access"
	"personal-game-server-manager/lambda/internal/server"
)

// AccessPage is everything the access page needs in one answer.
type AccessPage struct {
	Users   []access.User  `json:"users"`
	Servers []AccessServer `json:"servers"`
	Grants  []access.Grant `json:"grants"`
}

// AccessServer is a server as something to grant access to. Grants are keyed on the server id, so
// that is what identifies one here.
type AccessServer struct {
	ServerID   string `json:"ServerId"`
	GameName   string `json:"GameName"`
	ServerName string `json:"ServerName"`
	InstanceID string `json:"InstanceId"`
}

func getAccess(ctx context.Context, h *Handler, _ Request, servers []server.Server) (any, error) {
	// Each list is left empty rather than absent when it can't be read: the page is still usable for
	// whatever did load, which is worth more than an error.
	users, err := h.directory.Users(ctx)
	if err != nil {
		log.Printf("couldn't list users: %v", err)
	}
	if users == nil {
		users = []access.User{}
	}

	grants, err := h.grants.All(ctx)
	if err != nil {
		log.Printf("couldn't read the access grants: %v", err)
	}
	if grants == nil {
		grants = []access.Grant{}
	}

	grantable := make([]AccessServer, 0, len(servers))
	for _, s := range servers {
		// A server with no server-id tag can't be granted to anyone, because there is nothing stable
		// to key the grant on.
		if s.ServerID == "" {
			continue
		}
		grantable = append(grantable, AccessServer{
			ServerID:   s.ServerID,
			GameName:   s.GameName,
			ServerName: s.ServerName,
			InstanceID: s.InstanceID,
		})
	}

	return AccessPage{Users: users, Servers: grantable, Grants: grants}, nil
}

func saveAccess(ctx context.Context, h *Handler, req Request, _ []server.Server) (any, error) {
	changes := req.Body.Changes
	if err := h.grants.Save(ctx, changes, req.Sub); err != nil {
		return nil, err
	}

	saved := fmt.Sprintf("Saved access for %d server/user %s", len(changes), plural(len(changes), "pair"))

	renamed, err := h.directory.Rename(ctx, req.Body.Names)
	if err != nil {
		log.Printf("couldn't rename every user: %v", err)
	}
	if renamed > 0 {
		saved += fmt.Sprintf(", and renamed %d %s", renamed, plural(renamed, "user"))
	}
	return saved, nil
}

func plural(count int, word string) string {
	if count == 1 {
		return word
	}
	return word + "s"
}
