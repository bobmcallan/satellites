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

// Document is the latest-pointer row on documents. The struct unifies
// "document" (free-form authored substrate) and "story" (unit-of-work)
// behind a single shape, distinguished by Type. Story-only metadata
// (Tags, Status, Priority, Category, ParentID, AcceptanceCriteria,
// Summary) lives on the row directly; for type='document' rows these
// fields are at their zero value.
type Document struct {
	ID                 string     `json:"id"`
	Type               string     `json:"type"`
	Scope              Scope      `json:"scope"`
	WorkspaceID        string     `json:"workspace_id,omitempty"`
	ProjectID          string     `json:"project_id,omitempty"`
	Name               string     `json:"name"`
	LatestVersion      int        `json:"latest_version"`
	Tags               []string   `json:"tags"`
	Status             string     `json:"status"`
	Priority           string     `json:"priority,omitempty"`
	Category           string     `json:"category,omitempty"`
	ParentID           string     `json:"parent_id,omitempty"`
	AcceptanceCriteria string     `json:"acceptance_criteria,omitempty"`
	Summary            string     `json:"summary,omitempty"`
	SummaryUpdatedAt   *time.Time `json:"summary_updated_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// Type values for the documents.type discriminator.
const (
	TypeDocument = "document"
	TypeStory    = "story"
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
	Name        string
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
		if k.WorkspaceID != "" || k.ProjectID != "" {
			return ErrScopeMismatch
		}
	case ScopeWorkspace:
		if k.WorkspaceID == "" || k.ProjectID != "" {
			return ErrScopeMismatch
		}
	case ScopeProject:
		if k.WorkspaceID == "" || k.ProjectID == "" {
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
