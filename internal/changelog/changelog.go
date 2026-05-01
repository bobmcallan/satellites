// Package changelog ports the V3 changelog primitive into the V4
// project-scoped data model (sty_12af0bdc). A Changelog row records a
// release note for one binary (`Service`) within one project, with a
// version-from → version-to badge, an effective date, and a free-form
// markdown content body. The first line of Content is treated as the
// heading by the portal panel.
package changelog

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when a changelog lookup misses or membership
// scoping rejects the row.
var ErrNotFound = errors.New("changelog: not found")

// Changelog is the persistence shape. Service is free-form (`satellites`,
// `satellites-agent`, plus any future binary) — no enum is hard-coded
// in Go so a new binary can land without a code edit.
type Changelog struct {
	ID            string    `json:"id"`
	WorkspaceID   string    `json:"workspace_id"`
	ProjectID     string    `json:"project_id"`
	Service       string    `json:"service"`
	VersionFrom   string    `json:"version_from"`
	VersionTo     string    `json:"version_to"`
	Content       string    `json:"content"`
	EffectiveDate time.Time `json:"effective_date,omitempty"`
	CreatedBy     string    `json:"created_by"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// NewID returns a fresh changelog id in the canonical `chg_<8hex>` form.
func NewID() string {
	return fmt.Sprintf("chg_%s", uuid.NewString()[:8])
}
