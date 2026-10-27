// Package access decides what a caller may do to a server, and stores the grants that say so.
package access

// Capability is one thing a caller may do to a server.
type Capability string

const (
	View         Capability = "view"
	Lifecycle    Capability = "lifecycle"
	Download     Capability = "download"
	Content      Capability = "content"
	Backup       Capability = "backup"
	AutoShutdown Capability = "autoshutdown"
	Resize       Capability = "resize"
)

// Template is a named set of capabilities a user can be granted on a server. The sets live here
// rather than in the table, so the table holds only which template a user has on which server and
// editing a template changes it for everyone holding it.
type Template string

const (
	Player Template = "player"
	Owner  Template = "owner"
)

var templates = map[Template]Set{
	Player: {View, Lifecycle, Download},
	Owner:  {View, Lifecycle, Download, Content, Backup},
}

// admin is every capability. AutoShutdown and Resize are admin-only: they change what a server costs
// to run.
var admin = Set{View, Lifecycle, Download, Content, Backup, AutoShutdown, Resize}

// Set is the capabilities a caller holds on one server.
type Set []Capability

func (s Set) Has(c Capability) bool {
	for _, held := range s {
		if held == c {
			return true
		}
	}
	return false
}

func (s Set) Strings() []string {
	out := make([]string, 0, len(s))
	for _, c := range s {
		out = append(out, string(c))
	}
	return out
}

// KnownTemplate reports whether a stored or requested role names a template.
func KnownTemplate(role string) bool {
	_, ok := templates[Template(role)]
	return ok
}

// For resolves what a caller holds on one server. An admin holds everything; anyone else holds what
// their granted template allows, and nothing at all without a grant.
func For(isAdmin bool, role string) Set {
	if isAdmin {
		return admin
	}
	return templates[Template(role)]
}
