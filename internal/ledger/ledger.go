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
	KindStoryCreated   = "story_created"
	KindStoryUpdated   = "story_updated"
	KindReviewFinding  = "review_finding"
	KindSummaryUpdated = "summary_updated"
	KindComment        = "comment"
)

// Entry is one row of a story's ledger.
type Entry struct {
	ID        string          `json:"id"`
	StoryID   string          `json:"story_id"`
	Kind      string          `json:"kind"`
	Actor     string          `json:"actor,omitempty"`
	Body      string          `json:"body,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Refs      json.RawMessage `json:"refs,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

// NewID returns a fresh ledger-entry id in the `evt_<8hex>` form.
// The `evt_` prefix keeps ids visually distinct from sty_ /
// proj_ / doc_ when they appear in tool output.
func NewID() string {
	return fmt.Sprintf("evt_%s", uuid.NewString()[:8])
}
