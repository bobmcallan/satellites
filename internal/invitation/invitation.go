// Package invitation records pending email invitations to workspaces and
// projects (epic:user-admin, sty_0e88352a). An invite is claimed — turned
// into a membership row — when the verified email logs in, or immediately
// when the invitee already has an account.
package invitation

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/workspace"
)

// Scope values: an invitation targets either a workspace or a single project.
const (
	ScopeWorkspace = "workspace"
	ScopeProject   = "project"
)

// Status values for an invitation's lifecycle.
const (
	StatusPending  = "pending"
	StatusAccepted = "accepted"
	StatusRevoked  = "revoked"
)

var (
	// ErrNotFound is returned when an invitation lookup misses.
	ErrNotFound = errors.New("invitation: not found")
	// ErrInvalidScope is returned for an unknown scope string.
	ErrInvalidScope = errors.New("invitation: invalid scope")
	// ErrInvalidRole is returned when the role is not valid for the scope.
	ErrInvalidRole = errors.New("invitation: invalid role for scope")
	// ErrNotPending is returned when revoking an invite that is not pending.
	ErrNotPending = errors.New("invitation: not pending")
	// ErrDuplicate is returned when a pending invite for (email,target) exists.
	ErrDuplicate = errors.New("invitation: duplicate pending invitation")
)

// Invitation is one pending/accepted/revoked invite row.
type Invitation struct {
	ID          string     `json:"id"`
	Email       string     `json:"email"`
	Scope       string     `json:"scope"`
	WorkspaceID string     `json:"workspace_id,omitempty"`
	ProjectID   string     `json:"project_id,omitempty"`
	Role        string     `json:"role"`
	InvitedBy   string     `json:"invited_by,omitempty"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	AcceptedAt  *time.Time `json:"accepted_at,omitempty"`
}

// NewID returns a fresh invitation id in the canonical `inv_<8hex>` form.
func NewID() string { return fmt.Sprintf("inv_%s", uuid.NewString()[:8]) }

// IsValidScope reports whether s is a recognised scope.
func IsValidScope(s string) bool { return s == ScopeWorkspace || s == ScopeProject }

// validRoleForScope reports whether role is valid for the given scope's
// membership role set.
func validRoleForScope(scope, role string) bool {
	switch scope {
	case ScopeWorkspace:
		return workspace.IsValidRole(role)
	case ScopeProject:
		return project.IsValidProjectRole(role)
	}
	return false
}
