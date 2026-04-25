// Package repo is the satellites-v4 repo primitive — one git remote per
// project per principle pr_c52ba6e8 ("repo is a first-class primitive
// with a semantic index"). The Repo row tracks the remote, its default
// branch, and the cached state from the most recent jcodemunch index
// pass; the index itself lives outside satellites and is owned by
// jcodemunch (docs/architecture.md §7).
package repo

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Status enumerates the lifecycle states defined in docs/architecture.md §7.
const (
	StatusActive   = "active"
	StatusArchived = "archived"
)

// Repo is the per-project git remote + index-state record. Every field
// in docs/architecture.md §7 is represented here; nothing more, nothing
// less per pr_c25cc661 (five primitives per project).
type Repo struct {
	ID            string    `json:"id"`
	WorkspaceID   string    `json:"workspace_id"`
	ProjectID     string    `json:"project_id"`
	GitRemote     string    `json:"git_remote"`
	DefaultBranch string    `json:"default_branch"`
	HeadSHA       string    `json:"head_sha"`
	LastIndexedAt time.Time `json:"last_indexed_at"`
	IndexVersion  int       `json:"index_version"`
	SymbolCount   int       `json:"symbol_count"`
	FileCount     int       `json:"file_count"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// NewID returns a fresh repo id in the canonical `repo_<8hex>` form,
// matching the project-wide id convention (sty_, ctr_, …).
func NewID() string {
	return fmt.Sprintf("repo_%s", uuid.NewString()[:8])
}

// IsKnownStatus reports whether s is one of the documented status
// values. The store layer uses this to reject arbitrary strings on
// Create / Archive per AC.
func IsKnownStatus(s string) bool {
	return s == StatusActive || s == StatusArchived
}
