// Package task is the satellites-v4 task queue primitive per
// docs/architecture.md §4. A Task is a unit of orchestration work with
// a single origin (story_stage, scheduled, story_producing, event), a
// lifecycle (enqueued → claimed → in_flight → closed), and a
// workspace-scoped audit trail.
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
	OriginEvent          = "event"
)

// Status enum values per §4 "Lifecycle" diagram.
//
// StatusArchived is the post-retention state introduced by sty_dc2998c5.
// The nightly sweep flips closed rows older than the project retention
// window into archived; the row stays in place + ledger anchors are
// untouched, but the row falls out of the default task_list query.
const (
	StatusEnqueued = "enqueued"
	StatusClaimed  = "claimed"
	StatusInFlight = "in_flight"
	StatusClosed   = "closed"
	StatusArchived = "archived"
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

// Kind enum values. Empty defaults to KindWork.
const (
	KindWork   = "work"
	KindReview = "review"
)

var validOrigins = map[string]struct{}{
	OriginStoryStage: {}, OriginScheduled: {}, OriginStoryProducing: {},
	OriginEvent: {},
}

var validStatuses = map[string]struct{}{
	StatusEnqueued: {}, StatusClaimed: {}, StatusInFlight: {}, StatusClosed: {}, StatusArchived: {},
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
//
// ContractInstanceID binds a task to a parent CI. Plan CIs enqueue
// child tasks against themselves so the story view groups work per CI
// and downstream contracts can pull the work the plan agent decomposed.
//
// Iteration is the lap number for tasks bound to a contract that appears
// multiple times in a story's workflow (loop case from rejection-append).
// First task on (story_id, contract_name) gets iteration=1; the next
// loop's tasks get iteration=2, etc. Surfaced on task_list so renderers
// can show "develop #2" without joining through CIs. sty_78ddc67b.
type Task struct {
	ID                 string `json:"id"`
	WorkspaceID        string `json:"workspace_id"`
	ProjectID          string `json:"project_id,omitempty"`
	ContractInstanceID string `json:"contract_instance_id,omitempty"`
	// Kind classifies the task by its purpose so subscribers can filter
	// the queue without inspecting payloads. Today: "review" (consumed
	// by the embedded reviewer service) vs "" / "work" (everything
	// else). The taxonomy is deliberately small; sty_c1200f75 expands
	// it into a proper seed-driven lifecycle.
	Kind             string        `json:"kind,omitempty"`
	Iteration        int           `json:"iteration,omitempty"`
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
	ReclaimCount     int           `json:"reclaim_count,omitempty"`
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
//
// closed → archived is the retention sweep's transition (sty_dc2998c5).
// Archived is terminal — the row drops out of the default task_list
// query but the ledger anchors persist for audit.
func ValidTransition(from, to string) bool {
	switch from {
	case StatusEnqueued:
		return to == StatusClaimed || to == StatusClosed
	case StatusClaimed:
		return to == StatusInFlight || to == StatusClosed || to == StatusEnqueued
	case StatusInFlight:
		return to == StatusClosed
	case StatusClosed:
		return to == StatusArchived
	default:
		return false
	}
}
