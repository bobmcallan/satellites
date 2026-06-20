// Package ledger is V5's append-only event log over stories.
//
// It wraps the substrate's `evidence` table (migration 0001 +
// payload column from 0014). The DB enforces append-only at the
// row level via triggers — every Append insert succeeds or fails
// atomically, and UPDATE / DELETE against rows is refused.
//
// Naming: the wire-level verbs are `ledger_append` and
// `ledger_list` because that is the operator-facing nomenclature
// (story:sty_5baec353 used "ledger" generically). The underlying
// table stays named `evidence` to align with the architecture's
// four-primitive vocabulary (story / tools / review / evidence) —
// `evidence` IS the ledger primitive.
package ledger

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Kind discriminators for the canonical entry shapes the verb
// layer emits. Operators can append entries of any kind via
// ledger_append; these are the names the substrate itself uses.
const (
	KindStoryCreated = "story_created"
	KindStoryUpdated = "story_updated"
	// KindTaskCreated / KindTaskUpdated mark the lifecycle of a top-level
	// project task (type:task, tsk_; epic:project-tasks). Tasks share the
	// ledger primitive with stories but fire their own kinds — they do NOT
	// emit the story reviewer/summary signals.
	KindTaskCreated    = "task_created"
	KindTaskUpdated    = "task_updated"
	KindReviewFinding  = "review_finding"
	KindSummaryUpdated = "summary_updated"
	KindComment        = "comment"
	// KindStepSummary is the per-transition step summary the loop writes
	// after a gated transition: the prose a config-named summariser skill
	// produced, recorded next to the transition it describes (sty_2517f6b8).
	KindStepSummary = "step_summary"
)

// LogKindPrefix is the marker on rows the arbor LedgerHandler emits.
// Anything starting with this prefix is a log event (e.g. log:info,
// log:warn) and may be appended by a runner / executor key. An admin user
// keeps full append rights across all kinds.
const LogKindPrefix = "log:"

// Entry is one row of the evidence ledger. All correlation ids are
// optional — a row may be scoped to any subset of {story, project,
// workspace, session, run}. Story-only callers (legacy verb-emitted
// entries) leave the other ids blank; the operator-observability path
// (sty_0006f5f5) populates project/workspace/session/run from the
// arbor LogHandler's context.
type Entry struct {
	ID          string          `json:"id"`
	StoryID     string          `json:"story_id,omitempty"`
	ProjectID   string          `json:"project_id,omitempty"`
	WorkspaceID string          `json:"workspace_id,omitempty"`
	SessionID   string          `json:"session_id,omitempty"`
	RunID       string          `json:"run_id,omitempty"`
	Kind        string          `json:"kind"`
	Actor       string          `json:"actor,omitempty"`
	Body        string          `json:"body,omitempty"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	Refs        json.RawMessage `json:"refs,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

// NewID returns a fresh ledger-entry id in the `evt_<8hex>` form.
// The `evt_` prefix keeps ids visually distinct from sty_ /
// proj_ / doc_ when they appear in tool output.
func NewID() string {
	return fmt.Sprintf("evt_%s", uuid.NewString()[:8])
}
