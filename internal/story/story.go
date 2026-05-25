// Package story is V5's units-of-work primitive: every requirement,
// bug, or improvement an operator wants tracked lives as a story
// row bound to a project. Stories may declare a parent_id pointing
// at another story (epic → story decomposition) and carry tags +
// priority + status for portal filtering.
package story

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

const (
	StatusBacklog    = "backlog"
	StatusReady      = "ready"
	StatusInProgress = "in_progress"
	StatusReview     = "review"
	StatusDone       = "done"
	StatusCancelled  = "cancelled"

	PriorityCritical = "critical"
	PriorityHigh     = "high"
	PriorityMedium   = "medium"
	PriorityLow      = "low"
	PriorityNone     = "none"

	CategoryFeature        = "feature"
	CategoryBug            = "bug"
	CategoryImprovement    = "improvement"
	CategoryInfrastructure = "infrastructure"
)

// Story is a unit of work the operator wants tracked.
type Story struct {
	ID                 string     `json:"id"`
	ProjectID          string     `json:"project_id"`
	ParentID           string     `json:"parent_id,omitempty"`
	Title              string     `json:"title"`
	Body               string     `json:"body,omitempty"`
	AcceptanceCriteria string     `json:"acceptance_criteria,omitempty"`
	Status             string     `json:"status"`
	Priority           string     `json:"priority"`
	Category           string     `json:"category"`
	Tags               []string   `json:"tags"`
	Summary            string     `json:"summary,omitempty"`
	SummaryUpdatedAt   *time.Time `json:"summary_updated_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// NewID returns a fresh story id in the canonical `sty_<8hex>`
// form (mirroring v4).
func NewID() string {
	return fmt.Sprintf("sty_%s", uuid.NewString()[:8])
}
