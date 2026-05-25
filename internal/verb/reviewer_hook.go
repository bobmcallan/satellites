// Reviewer hook — bridges story_create / story_update writes to
// the reviewer registry. Reviewers run asynchronously (the verb
// response returns immediately); each finding is appended to the
// story's ledger so operators poll the ledger to discover them.

package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/reviewer"
	"github.com/bobmcallan/satellites/internal/story"
)

var (
	reviewerRegistry *reviewer.Registry
	// reviewerDispatch is the indirection seam tests use to assert
	// the hook fired without spawning a goroutine. Production wires
	// to spawnReviewers; tests can swap a synchronous variant via
	// SetReviewerDispatchForTest.
	reviewerDispatch = spawnReviewers
)

// SetReviewerRegistry wires the loaded reviewer registry into the
// verb package. Called from cmd/satellites-server on boot. Pass nil
// to disable reviewer dispatch (the hook becomes a no-op).
func SetReviewerRegistry(r *reviewer.Registry) { reviewerRegistry = r }

// SetReviewerDispatchForTest overrides the goroutine spawn with a
// synchronous variant so tests can assert findings landed in the
// ledger before the test returns. Pass nil to restore the default.
func SetReviewerDispatchForTest(fn func(ctx context.Context, s story.Story)) {
	if fn == nil {
		reviewerDispatch = spawnReviewers
		return
	}
	reviewerDispatch = fn
}

// dispatchReviewers is the post-write hook story_create + story_update
// call. It returns immediately; the actual reviewer + ledger work
// happens via the configurable dispatch (spawnReviewers in prod,
// synchronous in tests).
func dispatchReviewers(_ context.Context, s story.Story) {
	if reviewerRegistry == nil || len(reviewerRegistry.EnabledNames()) == 0 {
		return
	}
	if ledgerStore == nil {
		return
	}
	reviewerDispatch(context.Background(), s)
}

// spawnReviewers runs reviewers in a detached goroutine using a
// fresh background context — the caller's ctx may be cancelled
// (HTTP timeout, CLI exit) before the upstream LLM call returns.
// The reviewer must outlive the verb response or its findings are
// lost.
func spawnReviewers(_ context.Context, s story.Story) {
	go runReviewersSync(context.Background(), s)
}

// RunReviewersSyncForTest is the synchronous variant of the
// reviewer dispatch, exported solely so integration tests can
// assert findings landed in the ledger before the test returns.
// Production callers stay on dispatchReviewers / spawnReviewers.
func RunReviewersSyncForTest(ctx context.Context, s story.Story) {
	runReviewersSync(ctx, s)
}

// runReviewersSync is the work the goroutine performs. Exposed at
// package scope (lowercase) so synchronous test dispatchers can
// share the body without forking it.
func runReviewersSync(ctx context.Context, s story.Story) {
	if reviewerRegistry == nil || ledgerStore == nil {
		return
	}
	findings, errs := reviewerRegistry.RunAll(ctx, s)
	for name, err := range errs {
		arbor.Warn("reviewer failed", "reviewer", name, "story_id", s.ID, "err", err)
	}
	for name, fs := range findings {
		for _, f := range fs {
			payload := map[string]any{
				"reviewer": name,
				"severity": f.Severity,
				"code":     f.Code,
				"field":    f.Field,
				"message":  f.Message,
			}
			payloadBytes, _ := json.Marshal(payload)
			if _, err := ledgerStore.Append(ctx, ledger.AppendInput{
				StoryID: s.ID,
				Kind:    ledger.KindReviewFinding,
				Actor:   fmt.Sprintf("reviewer:%s", name),
				Body:    f.Message,
				Payload: payloadBytes,
			}, time.Now().UTC()); err != nil {
				arbor.Warn("reviewer finding ledger append failed",
					"reviewer", name, "story_id", s.ID, "err", err)
				continue
			}
			dispatchSummaryRegen(ctx, s.ID)
		}
	}
}
