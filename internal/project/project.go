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
	"strings"
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
	ID              string `json:"id"`
	WorkspaceID     string `json:"workspace_id"`
	Name            string `json:"name"`
	Description     string `json:"description,omitempty"`
	Type            string `json:"type,omitempty"`
	GitURLCanonical string `json:"git_url_canonical,omitempty"`
	// Tags is the project's free-form []string tag set, mirroring documents.
	// Classification (type:) and phase (phase:) are single-valued KV tags read
	// via internal/kvtag; Type and Phase are DERIVED from these on read.
	Tags []string `json:"tags,omitempty"`
	// Phase is DERIVED from the phase: tag (discovery|build|…), "" when unset.
	Phase         string     `json:"phase,omitempty"`
	OwnerUserID   string     `json:"owner_user_id,omitempty"`
	Status        string     `json:"status"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	SeedMD        string     `json:"seed_md,omitempty"`
	SeedUpdatedAt *time.Time `json:"seed_updated_at,omitempty"`
	// NonRepo is DERIVED, never stored (operator decision, sty_88a76023): a
	// project with no bound git remote IS a document-collection project. Set on
	// create and on every read so consumers (agents, portal, tooling) get an
	// explicit signal without re-deriving the rule.
	NonRepo bool `json:"non_repo"`
}

// DeriveNonRepo reports whether a project is a document-collection project — it
// has no bound git remote. The single source of the non_repo rule: a project
// with an empty git_url_canonical is non-repo. Kept in one place so create and
// every read agree.
func DeriveNonRepo(gitURLCanonical string) bool {
	return strings.TrimSpace(gitURLCanonical) == ""
}

// NewID returns a fresh project id in the canonical `proj_<8hex>`
// form (mirroring v4).
func NewID() string {
	return fmt.Sprintf("proj_%s", uuid.NewString()[:8])
}
