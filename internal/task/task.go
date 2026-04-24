// Package task is the satellites-v4 task queue primitive per
// docs/architecture.md §4. A Task is a unit of orchestration work with
// a single origin (story_stage, scheduled, story_producing,
// free_preplan, event), a lifecycle (enqueued → claimed → in_flight →
// closed), and a workspace-scoped audit trail.
//
// Principles: pr_75826278 (Tasks are the orchestration queue),
// pr_c25cc661 (Tasks are one of five primitives), pr_0779e5af
// (Workspace is the multi-tenant isolation primitive).
package task

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Origin enum values per §4 "Origins" table.
const (
	OriginStoryStage     = "story_stage"
	OriginScheduled      = "scheduled"
	OriginStoryProducing = "story_producing"
	OriginFreePreplan    = "free_preplan"
	OriginEvent          = "event"
)

// Status enum values per §4 "Lifecycle" diagram.
const (
	StatusEnqueued = "enqueued"
	StatusClaimed  = "claimed"
	StatusInFlight = "in_flight"
	StatusClosed   = "closed"
)

// Priority enum values — dispatcher pulls critical before high, etc.
const (
	PriorityCritical = "critical"
	PriorityHigh     = "high"
	PriorityMedium   = "medium"
	PriorityLow      = "low"
)

// Outcome enum values — empty until Status=closed.
const (
	OutcomeSuccess = "success"
	OutcomeFailure = "failure"
	OutcomeTimeout = "timeout"
)

var validOrigins = map[string]struct{}{
	OriginStoryStage: {}, OriginScheduled: {}, OriginStoryProducing: {},
	OriginFreePreplan: {}, OriginEvent: {},
}

var validStatuses = map[string]struct{}{
	StatusEnqueued: {}, StatusClaimed: {}, StatusInFlight: {}, StatusClosed: {},
}

var validPriorities = map[string]struct{}{
	PriorityCritical: {}, PriorityHigh: {}, PriorityMedium: {}, PriorityLow: {},
}

var validOutcomes = map[string]struct{}{
	OutcomeSuccess: {}, OutcomeFailure: {}, OutcomeTimeout: {},
}

// priorityRank orders priorities so Claim can pick critical first.
var priorityRank = map[string]int{
	PriorityCritical: 0,
	PriorityHigh:     1,
	PriorityMedium:   2,
	PriorityLow:      3,
}

// PriorityRank exposes the dispatcher ordering to callers who need to
// sort task slices themselves (e.g. reporting). Unknown priorities sort
// last.
func PriorityRank(p string) int {
	if r, ok := priorityRank[p]; ok {
		return r
	}
	return 999
}

// Task is one orchestration row. Fields match docs/architecture.md §4
// verbatim. Trigger + Payload are JSON-encoded raw bytes so the store
// layer doesn't require compile-time knowledge of every origin's shape.
type Task struct {
	ID               string        `json:"id"`
	WorkspaceID      string        `json:"workspace_id"`
	ProjectID        string        `json:"project_id,omitempty"`
	Origin           string        `json:"origin"`
	Trigger          []byte        `json:"trigger,omitempty"`
	Payload          []byte        `json:"payload,omitempty"`
	Status           string        `json:"status"`
	Priority         string        `json:"priority"`
	ClaimedBy        string        `json:"claimed_by,omitempty"`
	ClaimedAt        *time.Time    `json:"claimed_at,omitempty"`
	CompletedAt      *time.Time    `json:"completed_at,omitempty"`
	Outcome          string        `json:"outcome,omitempty"`
	LedgerRootID     string        `json:"ledger_root_id,omitempty"`
	ExpectedDuration time.Duration `json:"expected_duration,omitempty"`
	CreatedAt        time.Time     `json:"created_at"`
}

// NewID returns a fresh task id in the canonical `task_<8hex>` form.
func NewID() string {
	return fmt.Sprintf("task_%s", uuid.NewString()[:8])
}

// Validate returns the first invariant violation on t, or nil if t is
// well-formed. Used at the store layer before every write.
func (t Task) Validate() error {
	if t.WorkspaceID == "" {
		return errors.New("task: workspace_id required")
	}
	if _, ok := validOrigins[t.Origin]; !ok {
		return fmt.Errorf("task: invalid origin %q", t.Origin)
	}
	if _, ok := validStatuses[t.Status]; !ok {
		return fmt.Errorf("task: invalid status %q", t.Status)
	}
	if _, ok := validPriorities[t.Priority]; !ok {
		return fmt.Errorf("task: invalid priority %q", t.Priority)
	}
	if t.Outcome != "" {
		if _, ok := validOutcomes[t.Outcome]; !ok {
			return fmt.Errorf("task: invalid outcome %q", t.Outcome)
		}
		if t.Status != StatusClosed {
			return errors.New("task: outcome may only be set when status=closed")
		}
	}
	if t.Status == StatusClosed && t.Outcome == "" {
		return errors.New("task: outcome required when status=closed")
	}
	if t.Status == StatusClaimed || t.Status == StatusInFlight {
		if t.ClaimedBy == "" {
			return errors.New("task: claimed_by required when status=claimed|in_flight")
		}
	}
	return nil
}

// ValidTransition returns true when moving from → to is a legal Status
// transition per the §4 lifecycle diagram. Store-layer enforcement so
// the invariant survives callers that bypass happy-path helpers.
func ValidTransition(from, to string) bool {
	switch from {
	case StatusEnqueued:
		return to == StatusClaimed || to == StatusClosed
	case StatusClaimed:
		return to == StatusInFlight || to == StatusClosed || to == StatusEnqueued
	case StatusInFlight:
		return to == StatusClosed
	default:
		return false
	}
}
