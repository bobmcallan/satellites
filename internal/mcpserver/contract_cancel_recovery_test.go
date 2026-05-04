package mcpserver

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/reviewer"
	"github.com/bobmcallan/satellites/internal/story"
)

// TestContractCancel_RecoversAfterIterationCapExhausted reproduces the
// scenario `ldg_8c3e9e08` documented as the original substrate gap:
//
//  1. A plan CI is repeatedly rejected by the reviewer.
//  2. Once `reviewIterationCap` is exceeded, CommitReviewVerdict stops
//     auto-appending and flips the story to blocked.
//  3. The story is stuck — contract_resume rejects failed CIs,
//     orchestrator_compose_plan is idempotent on the story, and there
//     is no in-MCP escape.
//
// After this story (sty_3a59a6d7) lands, contract_cancel on the failed
// plan CI mints a fresh successor at the same slot AND clears the
// blocked story status, restoring forward motion without bypassing the
// substrate.
func TestContractCancel_RecoversAfterIterationCapExhausted(t *testing.T) {
	// Force the cap low so the test runs deterministically. Reset on
	// teardown so other tests in the package see the default.
	prior := os.Getenv("SATELLITES_REVIEW_ITERATION_CAP")
	t.Setenv("SATELLITES_REVIEW_ITERATION_CAP", "2")
	defer func() {
		if prior == "" {
			os.Unsetenv("SATELLITES_REVIEW_ITERATION_CAP")
		} else {
			os.Setenv("SATELLITES_REVIEW_ITERATION_CAP", prior)
		}
	}()

	f := newContractFixture(t)

	// Walk the parent story to in_progress so CommitReviewVerdict's
	// blocked-flip on cap exhaustion is a legal transition (backlog →
	// blocked is rejected by story.ValidTransition).
	if _, err := f.server.stories.UpdateStatus(f.ctx, f.storyID, story.StatusReady, f.caller.UserID, f.now, nil); err != nil {
		t.Fatalf("walk story to ready: %v", err)
	}
	if _, err := f.server.stories.UpdateStatus(f.ctx, f.storyID, story.StatusInProgress, f.caller.UserID, f.now, nil); err != nil {
		t.Fatalf("walk story to in_progress: %v", err)
	}

	// Seed iteration-1 plan CI in pending_review (the state
	// CommitReviewVerdict expects).
	plan1 := f.seedCIAtStatus(t, "plan", 0, contract.StatusPendingReview)

	// Iteration 1 → reject → auto-append iteration 2 (within cap).
	res1, err := f.server.CommitReviewVerdict(f.ctx, plan1.ID, reviewer.VerdictRejected, "iter1 reject", "", f.caller.UserID, f.now, nil)
	if err != nil {
		t.Fatalf("commit iter1: %v", err)
	}
	if res1.AppendedCIID == "" {
		t.Fatalf("expected appended successor on iter1, got %+v", res1)
	}

	// Move the auto-appended iteration-2 CI to pending_review so the
	// next CommitReviewVerdict has a target. The auto-append leaves the
	// new CI at status=ready.
	if _, err := f.server.contracts.UpdateStatus(f.ctx, res1.AppendedCIID, contract.StatusClaimed, f.caller.UserID, f.now, nil); err != nil {
		t.Fatalf("walk iter2 to claimed: %v", err)
	}
	if _, err := f.server.contracts.UpdateStatus(f.ctx, res1.AppendedCIID, contract.StatusPendingReview, f.caller.UserID, f.now, nil); err != nil {
		t.Fatalf("walk iter2 to pending_review: %v", err)
	}

	// Iteration 2 → reject → cap (2) reached → no successor + story
	// flips to blocked.
	res2, err := f.server.CommitReviewVerdict(f.ctx, res1.AppendedCIID, reviewer.VerdictRejected, "iter2 reject", "", f.caller.UserID, f.now, nil)
	if err != nil {
		t.Fatalf("commit iter2: %v", err)
	}
	if res2.AppendedCIID != "" {
		t.Fatalf("expected NO successor on iter2 (cap exhausted), got %s", res2.AppendedCIID)
	}
	if res2.BlockedReason == "" {
		t.Fatalf("expected blocked_reason on iter2, got %+v", res2)
	}

	// Confirm story is now blocked.
	st, err := f.server.stories.GetByID(f.ctx, f.storyID, nil)
	if err != nil {
		t.Fatalf("get story: %v", err)
	}
	if st.Status != story.StatusBlocked {
		t.Fatalf("story status: got %q want %q", st.Status, story.StatusBlocked)
	}

	// Recovery — contract_cancel on the cap-exhausted failed CI.
	body, text, isErr := f.callContractCancel(t, res1.AppendedCIID, "release the cap-exhausted block")
	if isErr {
		t.Fatalf("contract_cancel returned isError: %s", text)
	}
	successorID, _ := body["successor_ci_id"].(string)
	if successorID == "" {
		t.Fatalf("expected successor_ci_id, got %s", text)
	}

	// Story status must be unblocked.
	st2, _ := f.server.stories.GetByID(f.ctx, f.storyID, nil)
	if st2.Status == story.StatusBlocked {
		t.Fatalf("contract_cancel did not clear blocked story; status still %q", st2.Status)
	}

	// Successor must be at ready and pass the predecessor gate.
	successor, err := f.server.contracts.GetByID(f.ctx, successorID, nil)
	if err != nil {
		t.Fatalf("get successor: %v", err)
	}
	if successor.Status != contract.StatusReady {
		t.Fatalf("successor status: got %q want %q", successor.Status, contract.StatusReady)
	}

	// Verify response body includes story_status reflecting the unblock.
	if got, _ := body["story_status"].(string); got != story.StatusInProgress {
		t.Fatalf("response story_status: got %q want %q", got, story.StatusInProgress)
	}

	// Final sanity: the cancellation_ledger_id row exists.
	rowID, _ := body["cancellation_ledger_id"].(string)
	if rowID == "" {
		t.Fatalf("expected cancellation_ledger_id in response: %s", text)
	}
	_ = json.Marshal // keep encoding/json import live for future assertions
}
