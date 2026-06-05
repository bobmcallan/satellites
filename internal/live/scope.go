// Package live is the SSE trigger bus (sty_b6e39eb8): Postgres LISTEN/NOTIFY
// fanned out to per-user-scoped, trigger-only Server-Sent-Events streams.
//
// It is transport-neutral — no net/http. The HTTP /events handler lives in
// internal/server and drives a Hub from here; the Listener bridges Postgres
// NOTIFY into the Hub. The stream a browser sees carries only a topic string;
// the project/workspace ids that accompany a Notification are authz scope keys
// used to decide delivery and are never forwarded to the client.
package live

// Notification is one trigger: a topic plus the authorization-scope ids the
// topic implies. Topic is the only field a browser ever sees; ProjectID and
// WorkspaceID stay server-side and gate per-user delivery.
type Notification struct {
	Topic       string
	ProjectID   string
	WorkspaceID string
}

// Scope is a connected client's authorization. An admin sees every topic;
// everyone else is limited to the workspaces they belong to. A Notification
// with no workspace can only reach an admin.
type Scope struct {
	Admin      bool
	Workspaces map[string]bool
}

// NewScope builds a Scope from an admin flag and the user's workspace ids.
// An admin scope ignores the id list (sees everything).
func NewScope(admin bool, workspaceIDs []string) Scope {
	if admin {
		return Scope{Admin: true}
	}
	ws := make(map[string]bool, len(workspaceIDs))
	for _, id := range workspaceIDs {
		ws[id] = true
	}
	return Scope{Workspaces: ws}
}

// Allows reports whether a client with this scope may receive n.
func (s Scope) Allows(n Notification) bool {
	if s.Admin {
		return true
	}
	if n.WorkspaceID == "" {
		return false
	}
	return s.Workspaces[n.WorkspaceID]
}
