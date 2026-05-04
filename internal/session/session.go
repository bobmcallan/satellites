// Package session is the satellites-v4 session registry — the record of
// which Claude Code harness chat UUIDs the SessionStart hook has
// registered for which users. The process-order gate in
// `contract_claim` (slice 8.3) reads this registry to decide
// whether an incoming claim's session_id is one the server knows about
// and has seen recently enough to act on.
package session

import "time"

// StalenessDefault is the cut-off after which a session is treated as
// "stale" — a claim attempt against a session whose last_seen_at is
// older than this returns session_stale. Overridable via
// SATELLITES_SESSION_STALENESS (see handler).
const StalenessDefault = 30 * time.Minute

// Source enum values. Kept low-cardinality so logs can group them.
const (
	SourceSessionStart = "session_start"
	SourceEnforceHook  = "enforce_hook"
	SourceAPIKey       = "apikey"
)

// Session records a registered harness session. (UserID, SessionID) is
// the primary key. LastSeenAt is refreshed on every claim so an active
// agent keeps its slot alive.
type Session struct {
	UserID     string    `json:"user_id"`
	SessionID  string    `json:"session_id"`
	Source     string    `json:"source"`
	Registered time.Time `json:"registered_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	// WorkspaceID binds the session to a specific workspace once known.
	// Populated by session_register or by future verbs (e.g.
	// project_status) that resolve a project's workspace. Empty until
	// the session has been bound. story_798631fd.
	WorkspaceID string `json:"workspace_id,omitempty"`

	// ActiveProjectID names the project this session is currently bound
	// to. Stamped by `project_set` (sty_4db7c3a3) when the agent resolves
	// a project from the local repo's git remote. Empty until the
	// session has explicitly bound a project. Project-scoped MCP verbs
	// may default to this id when the caller omits project_id.
	ActiveProjectID string `json:"active_project_id,omitempty"`
}
