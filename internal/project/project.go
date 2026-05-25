// Package project is V5's projects domain: every story/tool/review/
// evidence row belongs to a project, and every project belongs to a
// workspace. Cross-project reads still happen within a workspace, but
// cross-workspace reads do not exist at the data layer.
//
// PR 2 of the workspaces/projects/stories slice: table + four CLI
// verbs (create / list / get / update). Story-level FK tightening to
// projects also lands in the same migration.
package project

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

const (
	StatusActive   = "active"
	StatusArchived = "archived"
)

// Project is the per-engagement container.
//
// SeedMD + SeedUpdatedAt carry the operator-pushed seed content from
// `.satellites/seeds/<workspace_id>/<project_id>/project.md`.
// SeedUpdatedAt is nil until the first `satellites seed push` lands;
// SeedMD is "" by default.
type Project struct {
	ID              string     `json:"id"`
	WorkspaceID     string     `json:"workspace_id"`
	Name            string     `json:"name"`
	Description     string     `json:"description,omitempty"`
	GitURLCanonical string     `json:"git_url_canonical,omitempty"`
	OwnerUserID     string     `json:"owner_user_id,omitempty"`
	Status          string     `json:"status"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	SeedMD          string     `json:"seed_md,omitempty"`
	SeedUpdatedAt   *time.Time `json:"seed_updated_at,omitempty"`
}

// NewID returns a fresh project id in the canonical `proj_<8hex>`
// form (mirroring v4).
func NewID() string {
	return fmt.Sprintf("proj_%s", uuid.NewString()[:8])
}
