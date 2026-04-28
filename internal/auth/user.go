// Package auth is the satellites-v4 authentication surface: user model,
// password hashing, in-memory session + user stores, cookie helpers, middleware,
// and login/logout handlers. OAuth providers arrive in a sibling story.
package auth

// User is the minimal authenticated identity. Future stories extend this
// with workspace memberships + provider metadata; for now the fields match
// the v4 auth story scope.
type User struct {
	ID             string
	Email          string
	DisplayName    string
	Provider       string // "local" | "google" | "github"
	HashedPassword string // bcrypt hash; empty for OAuth-only users
	// GlobalAdmin marks a user as a platform-tier admin who may operate
	// across workspaces. Resolved via either this persisted field OR the
	// SATELLITES_GLOBAL_ADMIN_EMAILS env list — see IsGlobalAdmin.
	// story_3548cde2.
	GlobalAdmin bool
}
