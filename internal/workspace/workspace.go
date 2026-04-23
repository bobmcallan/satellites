// Package workspace is the satellites-v4 multi-tenant isolation primitive.
// Every project and downstream primitive (documents, stories, tasks, ledger
// rows, repo references) belongs to exactly one workspace. Cross-workspace
// read paths do not exist at the data layer — see docs/architecture.md §8.
package workspace

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Status values for a Workspace.
const (
	StatusActive   = "active"
	StatusArchived = "archived"
)

// Role values for a Member.
const (
	RoleAdmin    = "admin"
	RoleMember   = "member"
	RoleReviewer = "reviewer"
	RoleViewer   = "viewer"
)

// Workspace is the top-level multi-tenant container.
type Workspace struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	OwnerUserID string    `json:"owner_user_id"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Member binds a user to a workspace at a given role.
type Member struct {
	WorkspaceID string    `json:"workspace_id"`
	UserID      string    `json:"user_id"`
	Role        string    `json:"role"`
	AddedAt     time.Time `json:"added_at"`
	AddedBy     string    `json:"added_by"`
}

// NewID returns a fresh workspace id in the canonical `wksp_<8hex>` form.
func NewID() string {
	return fmt.Sprintf("wksp_%s", uuid.NewString()[:8])
}

// IsValidRole reports whether r is one of the four recognised roles.
func IsValidRole(r string) bool {
	switch r {
	case RoleAdmin, RoleMember, RoleReviewer, RoleViewer:
		return true
	}
	return false
}
