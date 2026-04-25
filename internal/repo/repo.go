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
	WebhookSecret string    `json:"webhook_secret,omitempty"`
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

// Commit is a per-commit row keyed by (RepoID, SHA). Story_c2a2f073
// added this primitive so the portal repo view's recent-commits panel
// renders without hitting the ledger; per-story extraction lives on
// the StoryIDs slice.
type Commit struct {
	RepoID      string    `json:"repo_id"`
	SHA         string    `json:"sha"`
	Subject     string    `json:"subject"`
	Author      string    `json:"author,omitempty"`
	URL         string    `json:"url,omitempty"`
	CommittedAt time.Time `json:"committed_at"`
	ParentSHA   string    `json:"parent_sha,omitempty"`
	StoryIDs    []string  `json:"story_ids,omitempty"`
}

// SymbolChange annotates one symbol-level delta between two refs.
// Status is one of "added" / "removed" / "modified". Empty in v1 of
// Diff because the diff-content backend (GitHub Compare API) is a
// follow-up; the type ships now so the portal/MCP shape is stable.
type SymbolChange struct {
	SymbolID string `json:"symbol_id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Status   string `json:"status"`
	File     string `json:"file,omitempty"`
}

// DiffSourceUnavailable marks a Diff that does not carry real
// file-diff content because satellites cannot derive it inside the
// substrate (per pr_c52ba6e8 — no cloning; webhook payloads do not
// carry diffs). The commits-between-refs walk is still authoritative.
const DiffSourceUnavailable = "unavailable"

// Diff is the result of branch-comparison between FromRef and ToRef
// on a tracked repo. Commits is the parent-walk from ToRef back to
// FromRef (or until the chain ends). Unified + SymbolChanges remain
// empty in v1 with DiffSource=DiffSourceUnavailable until a follow-up
// story integrates the GitHub Compare API.
type Diff struct {
	RepoID           string         `json:"repo_id"`
	FromRef          string         `json:"from_ref"`
	ToRef            string         `json:"to_ref"`
	Commits          []Commit       `json:"commits"`
	Unified          string         `json:"unified"`
	SymbolChanges    []SymbolChange `json:"symbol_changes"`
	DiffSource       string         `json:"diff_source"`
	DiffSourceReason string         `json:"diff_source_reason,omitempty"`
}
