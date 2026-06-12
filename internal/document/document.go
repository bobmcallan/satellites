// Package document is V5's documents-as-substrate primitive: a
// scope-aware, versioned blob store. Every operator-editable artifact
// (seeds, principles, runbooks, rubrics) becomes a row here, retrieved
// through the document_get verb instead of a bespoke typed verb.
//
// Three scopes:
//
//	system    — ships embedded/seeded at boot. Read-only via user-facing
//	            verbs; writes from non-seed callers return ErrScopeReadonly.
//	workspace — bound to a workspace; operator-editable.
//	project   — bound to a (workspace, project) pair; operator-editable.
//
// Every upsert appends a new document_versions row and advances
// documents.latest_version. Deletes are soft (status='deleted' on the
// latest version); historical versions stay retrievable via version=all.
package document

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Scope is the typed column on the documents row.
type Scope string

const (
	ScopeSystem    Scope = "system"
	ScopeWorkspace Scope = "workspace"
	ScopeProject   Scope = "project"
	// ScopeUser is the highest-precedence override layer (sty_cbeeb452).
	// A user-scope row is keyed (user_id, name) and shadows the same-named
	// project/workspace/system row for that caller, so a user can adapt the
	// process without editing the shared rows. user_id is the caller
	// identity, never a request-body field.
	ScopeUser Scope = "user"
	// ScopeLibrary is the shared distribution surface (epic:skill-library,
	// sty_5678cc4b): published artifacts readable by every authenticated
	// caller, writable only into the caller's own publisher namespace.
	// Publisher = the publishing project, so a library row is keyed
	// (project_id, name) with workspace_id NULL — it deliberately escapes
	// the workspace boundary. Library is NOT a layer of the inherit
	// cascade; reads name it explicitly.
	ScopeLibrary Scope = "library"
)

// Status is the typed column on document_versions.
type Status string

const (
	StatusActive  Status = "active"
	StatusDeleted Status = "deleted"
)

// ErrScopeReadonly is returned when a non-seed caller tries to write
// at scope=system. System-scope documents ship embedded or seed at
// boot; mutating them is a build-time/repo-edit operation, not a
// runtime verb.
var ErrScopeReadonly = errors.New("document: system scope is read-only")

// ErrNotFound is returned when a document lookup misses.
var ErrNotFound = errors.New("document: not found")

// ErrScopeMismatch is returned when the (scope, workspace_id, project_id)
// triple does not satisfy the scope-coherence rules.
var ErrScopeMismatch = errors.New("document: scope coherence violation")

// ErrTypeMismatch is returned when an upsert names a type that
// disagrees with an existing row at the same (scope, workspace_id,
// project_id, name). Document and skill share a namespace so the key
// resolves to at most one row; the caller may not silently flip its
// type via upsert.
var ErrTypeMismatch = errors.New("document: type mismatch on existing key")

// Document is the latest-pointer row on documents. The struct unifies
// "document" (free-form authored substrate) and "story" (unit-of-work)
// behind a single shape, distinguished by Type. Story-only metadata
// (Tags, Status, Priority, Category, ParentID, AcceptanceCriteria,
// Summary) lives on the row directly; for type='document' rows these
// fields are at their zero value.
type Document struct {
	ID                 string   `json:"id"`
	Type               string   `json:"type"`
	Scope              Scope    `json:"scope"`
	WorkspaceID        string   `json:"workspace_id,omitempty"`
	ProjectID          string   `json:"project_id,omitempty"`
	UserID             string   `json:"user_id,omitempty"`
	Name               string   `json:"name"`
	LatestVersion      int      `json:"latest_version"`
	Tags               []string `json:"tags"`
	Status             string   `json:"status"`
	Priority           string   `json:"priority,omitempty"`
	Category           string   `json:"category,omitempty"`
	ParentID           string   `json:"parent_id,omitempty"`
	AcceptanceCriteria string   `json:"acceptance_criteria,omitempty"`
	Headline           string   `json:"headline,omitempty"`
	// Audience gates library reads ('all' by default; a non-'all' row is
	// readable only by its publisher). Set by the DB default — no write
	// surface ships with the initial library.
	Audience         string     `json:"audience,omitempty"`
	Summary          string     `json:"summary,omitempty"`
	SummaryUpdatedAt *time.Time `json:"summary_updated_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// Type values for the documents.type discriminator.
const (
	TypeDocument = "document"
	TypeStory    = "story"
	TypeTask     = "task"
	TypeSkill    = "skill"
)

// Version is an immutable row from document_versions.
type Version struct {
	DocumentID string    `json:"document_id"`
	Version    int       `json:"version"`
	Body       string    `json:"body"`
	Status     Status    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	CreatedBy  string    `json:"created_by,omitempty"`
}

// Key identifies a document within its scope.
type Key struct {
	Scope       Scope
	WorkspaceID string
	ProjectID   string
	// UserID keys a user-scope override row (sty_cbeeb452). Set only for
	// ScopeUser; empty for every other scope. It carries the caller
	// identity, never a request-body value.
	UserID string
	Name   string
}

// Validate enforces the scope-coherence rules the DB CHECK constraint
// also enforces. Surfacing it here lets the store fail fast with a
// readable error before the round-trip.
func (k Key) Validate() error {
	if k.Name == "" {
		return fmt.Errorf("document: name required")
	}
	switch k.Scope {
	case ScopeSystem:
		if k.WorkspaceID != "" || k.ProjectID != "" || k.UserID != "" {
			return ErrScopeMismatch
		}
	case ScopeWorkspace:
		if k.WorkspaceID == "" || k.ProjectID != "" || k.UserID != "" {
			return ErrScopeMismatch
		}
	case ScopeProject:
		if k.WorkspaceID == "" || k.ProjectID == "" || k.UserID != "" {
			return ErrScopeMismatch
		}
	case ScopeUser:
		if k.UserID == "" || k.WorkspaceID != "" || k.ProjectID != "" {
			return ErrScopeMismatch
		}
	case ScopeLibrary:
		if k.ProjectID == "" || k.WorkspaceID != "" || k.UserID != "" {
			return ErrScopeMismatch
		}
	default:
		return fmt.Errorf("document: unknown scope %q", k.Scope)
	}
	return nil
}

// NewID returns a fresh document id in the canonical `doc_<8hex>` form.
func NewID() string {
	return fmt.Sprintf("doc_%s", uuid.NewString()[:8])
}
