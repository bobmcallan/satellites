// Package rolegrant is the satellites-v4 role-grant primitive: the
// authorisation currency for MCP verbs per docs/architecture.md §9
// Role-grant handshake. A grant binds one grantee (session, task, or
// worker) to one role under one agent document, workspace-scoped, with
// append-only release semantics.
//
// Many grants can coexist per role per workspace by design — two Claude
// sessions both hold orchestrator grants with distinct grantee_ids, and
// release of one does not affect the other. This shape is what resolves
// the v3 multi-session session_id singleton collision.
//
// No Delete verb: grants persist for audit per principle pr_0c11b762
// ("Evidence is the primary trust leverage"). The lifecycle is
// create → active → released, with release setting ReleasedAt.
package rolegrant

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Status enum values.
const (
	StatusActive   = "active"
	StatusReleased = "released"
)

// Grantee kind enum values. A grant's grantee is the entity that
// receives the scoped MCP verbs.
const (
	GranteeSession = "session"
	GranteeTask    = "task"
	GranteeWorker  = "worker"
)

var validStatuses = map[string]struct{}{
	StatusActive:   {},
	StatusReleased: {},
}

var validGranteeKinds = map[string]struct{}{
	GranteeSession: {},
	GranteeTask:    {},
	GranteeWorker:  {},
}

// RoleGrant is one row in the append-only role_grant table. Fields match
// docs/architecture.md §2 Role ("role_grant" schema) and §9 Role-grant
// handshake.
type RoleGrant struct {
	ID          string     `json:"id"`
	WorkspaceID string     `json:"workspace_id"`
	ProjectID   *string    `json:"project_id,omitempty"`
	RoleID      string     `json:"role_id"`
	AgentID     string     `json:"agent_id"`
	GranteeKind string     `json:"grantee_kind"`
	GranteeID   string     `json:"grantee_id"`
	Status      string     `json:"status"`
	IssuedAt    time.Time  `json:"issued_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	ReleasedAt  *time.Time `json:"released_at,omitempty"`
	ReleaseNote string     `json:"release_note,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// NewID returns a fresh role-grant id in the canonical `grant_<8hex>`
// form.
func NewID() string {
	return fmt.Sprintf("grant_%s", uuid.NewString()[:8])
}

// Validate returns the first invariant violation on g, or nil if g is
// well-formed. FK existence on RoleID + AgentID is enforced at the store
// layer at Create time.
func (g RoleGrant) Validate() error {
	if g.WorkspaceID == "" {
		return errors.New("rolegrant: workspace_id is required")
	}
	if g.RoleID == "" {
		return errors.New("rolegrant: role_id is required")
	}
	if g.AgentID == "" {
		return errors.New("rolegrant: agent_id is required")
	}
	if g.GranteeID == "" {
		return errors.New("rolegrant: grantee_id is required")
	}
	if _, ok := validGranteeKinds[g.GranteeKind]; !ok {
		return fmt.Errorf("rolegrant: invalid grantee_kind %q", g.GranteeKind)
	}
	if _, ok := validStatuses[g.Status]; !ok {
		return fmt.Errorf("rolegrant: invalid status %q", g.Status)
	}
	if g.Status == StatusReleased && g.ReleasedAt == nil {
		return errors.New("rolegrant: released_at required when status=released")
	}
	if g.Status == StatusActive && g.ReleasedAt != nil {
		return errors.New("rolegrant: released_at must be nil when status=active")
	}
	return nil
}
