// Summary hook — bridges every ledger append for a story to a
// regeneration of that story's summary field. Reuses the same
// reviewer registry as the finding-emitting reviewer (B3); the
// summary reviewer is just another markdown definition whose
// output is free-form prose instead of JSON findings.

package verb

import (
	"context"
	"time"

	"github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/reviewer"
)

// SummaryReviewerName is the registry key for the summary
// definition. The runtime looks it up by name; the file name on
// disk is incidental.
const SummaryReviewerName = "satellites-story-summary"

var (
	// summaryDispatch is the indirection seam tests use to assert
	// the hook fired without spawning a goroutine. Production wires
	// to spawnSummaryRegen; tests can swap a synchronous variant
	// via SetSummaryDispatchForTest.
	summaryDispatch = spawnSummaryRegen
)

// SetSummaryDispatchForTest overrides the goroutine spawn with a
// synchronous variant. Pass nil to restore the default.
func SetSummaryDispatchForTest(fn func(ctx context.Context, storyID string)) {
	if fn == nil {
		summaryDispatch = spawnSummaryRegen
		return
	}
	summaryDispatch = fn
}

// dispatchSummaryRegen is the post-append hook called from every
// site that writes a ledger entry. It returns immediately; the
// LLM call + UPDATE happen via the configurable dispatch.
func dispatchSummaryRegen(_ context.Context, storyID string) {
	if reviewerRegistry == nil {
		return
	}
	def, ok := reviewerRegistry.Defs[SummaryReviewerName]
	if !ok || !def.Enabled {
		return
	}
	if reviewerRegistry.Client == nil {
		return
	}
	if documentStore == nil || ledgerStore == nil {
		return
	}
	if storyID == "" {
		return
	}
	summaryDispatch(context.Background(), storyID)
}

// spawnSummaryRegen is the production dispatch: fire a goroutine
// against a fresh background context. The caller's ctx may be
// cancelled before the LLM call returns; the regeneration must
// outlive the verb response.
func spawnSummaryRegen(_ context.Context, storyID string) {
	go regenerateSummary(context.Background(), storyID)
}

// regenerateSummary loads the current story + its full ledger,
// renders the summary prompt, calls the LLM, and writes the
// resulting prose to stories.summary. Errors are logged and
// swallowed — a failing summary regen never breaks the
// originating verb.
func regenerateSummary(ctx context.Context, storyID string) {
	if reviewerRegistry == nil || documentStore == nil || ledgerStore == nil {
		return
	}
	def, ok := reviewerRegistry.Defs[SummaryReviewerName]
	if !ok || !def.Enabled || reviewerRegistry.Client == nil {
		return
	}
	d, body, err := documentStore.GetByIDWithLatestBody(ctx, storyID)
	if err != nil {
		arbor.Warn("summary regen: get story failed", "story_id", storyID, "err", err)
		return
	}
	s := NewStoryEnvelope(d, body)
	entries, err := ledgerStore.ListByStory(ctx, storyID, "")
	if err != nil {
		arbor.Warn("summary regen: list ledger failed", "story_id", storyID, "err", err)
		return
	}
	envelope := summaryEnvelope{Story: s, Ledger: entries}
	text, err := reviewer.RunText(ctx, def, reviewerRegistry.Client, envelope)
	if err != nil {
		arbor.Warn("summary regen: reviewer failed", "story_id", storyID, "err", err)
		return
	}
	if err := documentStore.SetSummary(ctx, storyID, text, time.Now().UTC()); err != nil {
		arbor.Warn("summary regen: set summary failed", "story_id", storyID, "err", err)
	}
}

// summaryEnvelope is the JSON shape passed to the summary
// reviewer's prompt template. Fields names match the schema the
// markdown body documents to the LLM.
type summaryEnvelope struct {
	Story  any            `json:"story"`
	Ledger []ledger.Entry `json:"ledger"`
}

// RegenerateSummaryForTest is the test-facing synchronous variant.
// Exposed so integration tests can drive the regen without
// goroutine races.
func RegenerateSummaryForTest(ctx context.Context, storyID string) {
	regenerateSummary(ctx, storyID)
}
