package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/verb"
)

// TestClaimLeaseTTLOutlivesDispatch pins that the local work-claim lease
// outlives the gate dispatch it covers. A gate run (build + test) can take up
// to gateDispatchTimeout; if the lease expired first, a second reviewer could
// reclaim the work area mid-run while the gate is still writing its
// post-decision spine + step_summary rows. The TTL is derived from the
// timeout + headroom so the two cannot drift; this test fails if a future
// edit lets the lease fall to or below the dispatch timeout.
func TestClaimLeaseTTLOutlivesDispatch(t *testing.T) {
	if claimLeaseTTL <= gateDispatchTimeout {
		t.Fatalf("claimLeaseTTL (%s) must exceed gateDispatchTimeout (%s) so a long gate run keeps its work-claim lease",
			claimLeaseTTL, gateDispatchTimeout)
	}
	if headroom := claimLeaseTTL - gateDispatchTimeout; headroom < time.Minute {
		t.Fatalf("claimLeaseTTL headroom over the dispatch timeout is only %s; want at least 1m of slack", headroom)
	}
}

// TestRunReviewRequiresSkill pins the new model: --skill is required. The
// client holds no workflow knowledge and never resolves a gate from status —
// it must be told which gate to run. Without --skill, runReview errors before
// any substrate read or gate dispatch.
func TestRunReviewRequiresSkill(t *testing.T) {
	err := runReview(t.Context(), reviewOpts{
		StoryID: "sty_deadbeef",
		Skill:   "", // missing
	})
	if err == nil {
		t.Fatal("runReview with no --skill should error")
	}
	if got := err.Error(); got != "--skill is required: name the gate skill to run" {
		t.Fatalf("error = %q, want the --skill-required message", got)
	}
}

// TestRunSatellitesActorCommand pins AC4's decision mapping: exit 0 → accept,
// non-zero → reject, with the command + output recorded as evidence notes.
func TestRunSatellitesActorCommand(t *testing.T) {
	ctx := context.Background()
	out := runSatellitesActorCommand(ctx, reviewOpts{}, "echo all-clear; true")
	if out.Decision != verb.GateDecisionAccept {
		t.Fatalf("exit 0 must accept: %+v", out)
	}
	if !strings.Contains(out.Notes, "all-clear") || !strings.Contains(out.Notes, "exit 0") {
		t.Fatalf("accept notes must carry command evidence: %q", out.Notes)
	}
	out = runSatellitesActorCommand(ctx, reviewOpts{}, "echo broken-window; exit 3")
	if out.Decision != verb.GateDecisionReject {
		t.Fatalf("non-zero exit must reject: %+v", out)
	}
	if !strings.Contains(out.Notes, "broken-window") || !strings.Contains(out.Notes, "failed") {
		t.Fatalf("reject notes must carry command evidence: %q", out.Notes)
	}
}
