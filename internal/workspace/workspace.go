// Package workspace is V5's multi-tenant root. Every project (and
// transitively every story/tool/review/evidence row) belongs to exactly
// one workspace. Cross-workspace read paths do not exist at the data
// layer — that isolation is what makes workspace the unit of admin,
// billing, and access control.
//
// PR 1 establishes the table + four CLI verbs (create / list / get /
// set_default). Membership and per-owner defaults follow in subsequent
// PRs; today the system runs single-tenant with a NULL-owner default
// seeded at boot.
package workspace

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

const (
	StatusActive   = "active"
	StatusArchived = "archived"
)

// Workspace is the multi-tenant root row.
type Workspace struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	OwnerUserID string    `json:"owner_user_id,omitempty"`
	Status      string    `json:"status"`
	IsDefault   bool      `json:"is_default"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// NewID returns a fresh workspace id in the canonical `wksp_<8hex>`
// form (mirroring v4).
func NewID() string {
	return fmt.Sprintf("wksp_%s", uuid.NewString()[:8])
}
