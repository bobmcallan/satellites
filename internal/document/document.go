// Package document is the satellites-v4 document primitive: a unified
// row in SurrealDB discriminated by `type` per docs/architecture.md §2.
// One schema covers artifacts, contracts, skills, principles, and
// reviewers; per-type behaviour layers on top of the shared store.
package document

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// Type enum values per docs/architecture.md §2 Documents sub-section.
const (
	TypeArtifact  = "artifact"
	TypeContract  = "contract"
	TypeSkill     = "skill"
	TypePrinciple = "principle"
	TypeReviewer  = "reviewer"
	TypeAgent     = "agent"
	TypeRole      = "role"
)

// Scope enum values per docs/architecture.md §2.
const (
	ScopeSystem    = "system"
	ScopeProject   = "project"
	ScopeWorkspace = "workspace"
)

// Status enum values.
const (
	StatusActive   = "active"
	StatusArchived = "archived"
)

var validTypes = map[string]struct{}{
	TypeArtifact:  {},
	TypeContract:  {},
	TypeSkill:     {},
	TypePrinciple: {},
	TypeReviewer:  {},
	TypeAgent:     {},
	TypeRole:      {},
}

var validScopes = map[string]struct{}{
	ScopeSystem:    {},
	ScopeProject:   {},
	ScopeWorkspace: {},
}

// Document is the unified, type-discriminated row backing every authored
// content kind in satellites-v4. Every row scopes to exactly one workspace
// (per docs/architecture.md §8) and — when Scope=ScopeProject — to exactly
// one project (per principle pr_7ade92ae). Scope=ScopeSystem rows have nil
// ProjectID and are globally readable inside their workspace.
type Document struct {
	ID              string    `json:"id"`
	WorkspaceID     string    `json:"workspace_id"`
	ProjectID       *string   `json:"project_id,omitempty"`
	Type            string    `json:"type"`
	Name            string    `json:"name"`
	Body            string    `json:"body"`
	Structured      []byte    `json:"structured,omitempty"`
	ContractBinding *string   `json:"contract_binding,omitempty"`
	Scope           string    `json:"scope"`
	Tags            []string  `json:"tags,omitempty"`
	Status          string    `json:"status"`
	BodyHash        string    `json:"body_hash"`
	Version         int       `json:"version"`
	CreatedAt       time.Time `json:"created_at"`
	CreatedBy       string    `json:"created_by,omitempty"`
	UpdatedAt       time.Time `json:"updated_at"`
	UpdatedBy       string    `json:"updated_by,omitempty"`
}

// Validate returns the first invariant violation on d, or nil if d is
// well-formed. Validate covers shape only; FK existence on
// ContractBinding is enforced by the store at write time.
func (d Document) Validate() error {
	if _, ok := validTypes[d.Type]; !ok {
		return fmt.Errorf("document: invalid type %q", d.Type)
	}
	if _, ok := validScopes[d.Scope]; !ok {
		return fmt.Errorf("document: invalid scope %q", d.Scope)
	}
	switch d.Scope {
	case ScopeProject:
		if d.ProjectID == nil || *d.ProjectID == "" {
			return errors.New("document: project_id required when scope=project")
		}
	case ScopeSystem:
		if d.ProjectID != nil && *d.ProjectID != "" {
			return errors.New("document: project_id must be nil when scope=system")
		}
	case ScopeWorkspace:
		if d.Type != TypeRole {
			return fmt.Errorf("document: scope=workspace only valid for type=role, got type=%s", d.Type)
		}
		if d.WorkspaceID == "" {
			return errors.New("document: workspace_id required when scope=workspace")
		}
		if d.ProjectID != nil && *d.ProjectID != "" {
			return errors.New("document: project_id must be nil when scope=workspace")
		}
	}
	switch d.Type {
	case TypeSkill, TypeReviewer:
		if d.ContractBinding == nil || *d.ContractBinding == "" {
			return fmt.Errorf("document: contract_binding required for type=%s", d.Type)
		}
	case TypeAgent:
		// agent documents may optionally pin to a contract via contract_binding
		// (mirrors reviewer); the field is permitted but not required.
	default:
		if d.ContractBinding != nil && *d.ContractBinding != "" {
			return errors.New("document: contract_binding allowed only for type=skill, type=reviewer, or type=agent")
		}
	}
	return nil
}

// HashBody returns a sha256 content hash prefixed with "sha256:"; used as
// the equality test for Upsert's idempotence check.
func HashBody(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// StringPtr returns nil for the empty string, otherwise a pointer to a
// fresh copy of s. Callers building Documents use it to honour the
// "ProjectID nil when scope=system" invariant without sprinkling
// conditional pointer construction across the call sites.
func StringPtr(s string) *string {
	if s == "" {
		return nil
	}
	v := s
	return &v
}
