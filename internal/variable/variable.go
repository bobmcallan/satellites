// Package variable is V5's name/value substrate, scoped the same way
// as documents (system, workspace, project) but without versioning —
// variables are point-in-time configuration, not append-only artifacts.
//
// Two flavours coexist at scope='system':
//   - Computed (version, os, arch, server_url, state, current_version)
//     are resolved per-request by the templating layer and never stored.
//   - Stored (operator-tunable knobs like stories.page_size) live in
//     the variables table at scope='system' with workspace_id/
//     project_id NULL. Seeded at boot, editable via variable_set.
//
// The verb-layer resolver checks stored-system rows first and falls
// back to the computed resolver, so a name collision is impossible
// against the computed set and a missing seed gracefully degrades to
// the computed terminator (which returns ok=false for unknown names).
package variable

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Scope is the typed column on variables.
type Scope string

const (
	ScopeSystem    Scope = "system"
	ScopeWorkspace Scope = "workspace"
	ScopeProject   Scope = "project"
)

// ErrNotFound is returned when a variable lookup misses.
var ErrNotFound = errors.New("variable: not found")

// ErrScopeReadonly is retained for backwards compatibility with callers
// that branched on it before stored-system rows existed. New code uses
// ErrComputedName for the narrower case — a write to a name owned by
// the computed-system resolver.
var ErrScopeReadonly = errors.New("variable: system scope is read-only (computed, not stored)")

// ErrComputedName is returned when a write targets a system name owned
// by the computed-variables resolver (version, os, arch, …). Those
// names map to per-request derived state and cannot be persisted.
var ErrComputedName = errors.New("variable: name is computed at request time, not storable")

// ErrScopeMismatch is returned when (scope, workspace_id, project_id)
// disagree per the scope-coherence rules.
var ErrScopeMismatch = errors.New("variable: scope coherence violation")

// Variable is one stored name/value row.
type Variable struct {
	ID          string    `json:"id"`
	Scope       Scope     `json:"scope"`
	WorkspaceID string    `json:"workspace_id,omitempty"`
	ProjectID   string    `json:"project_id,omitempty"`
	Name        string    `json:"name"`
	Value       string    `json:"value"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Key identifies a variable within its scope. Same shape as
// document.Key — kept independent so the two namespaces can evolve.
type Key struct {
	Scope       Scope
	WorkspaceID string
	ProjectID   string
	Name        string
}

// Validate enforces the scope-coherence rules the DB CHECK also
// enforces — surfaces fast feedback to the caller before a round-trip.
func (k Key) Validate() error {
	if k.Name == "" {
		return fmt.Errorf("variable: name required")
	}
	switch k.Scope {
	case ScopeSystem:
		// system scope is read-only and never reaches the DB; we still
		// accept the Key shape for variable_get(inherit=true) where the
		// resolver layer terminates at scope=system.
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
		return fmt.Errorf("variable: unknown scope %q", k.Scope)
	}
	return nil
}

// NewID returns a fresh variable id in the canonical `var_<8hex>` form.
func NewID() string {
	return fmt.Sprintf("var_%s", uuid.NewString()[:8])
}
