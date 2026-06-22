package cli

import (
	"context"
	"encoding/json"
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

// TestRunReviewRequiresSkill pins the model: --skill is required unless
// --checkpoint is given. The client holds no workflow knowledge and never
// resolves a gate from status — it must be told which gate to run. Without
// either, runReview errors before any substrate read or gate dispatch.
func TestRunReviewRequiresSkill(t *testing.T) {
	err := runReview(t.Context(), reviewOpts{
		StoryID: "sty_deadbeef",
		Skill:   "", // missing, and no --checkpoint
	})
	if err == nil {
		t.Fatal("runReview with no --skill and no --checkpoint should error")
	}
	if got := err.Error(); !strings.Contains(got, "--skill is required") {
		t.Fatalf("error = %q, want the --skill-required message", got)
	}
}

// TestRunReviewSkillAndCheckpointConflict pins that --skill and --checkpoint are
// mutually exclusive: a checkpoint is the executor's deliberate move, never a
// side-effect of naming a gate (sty_21d2c535). Errors before any substrate read.
func TestRunReviewSkillAndCheckpointConflict(t *testing.T) {
	err := runReview(t.Context(), reviewOpts{
		StoryID:    "sty_deadbeef",
		Skill:      "satellites-intent-plan-review",
		Checkpoint: true,
	})
	if err == nil {
		t.Fatal("runReview with both --skill and --checkpoint should error")
	}
	if got := err.Error(); !strings.Contains(got, "not both") {
		t.Fatalf("error = %q, want the mutual-exclusion message", got)
	}
}

// TestCheckpointDecision pins the four-quadrant contract of the --checkpoint
// rule (sty_21d2c535): the ungated checkpoint hop fires ONLY on an explicit
// --checkpoint at a pure-checkpoint state; naming --skill at such a state errors
// (it must not silently transition — the bug this story fixes); --checkpoint at
// a non-checkpoint state errors; and a gate request elsewhere proceeds normally.
func TestCheckpointDecision(t *testing.T) {
	// checkpoint state + --checkpoint → enact.
	if enact, err := checkpointDecision(true, true, "in_progress", "shipping", "", ""); err != nil || !enact {
		t.Fatalf("checkpoint+checkpoint-state: enact=%v err=%v, want enact=true err=nil", enact, err)
	}
	// checkpoint state + --skill → error, NO enact (the silent-shadow bug).
	enact, err := checkpointDecision(false, true, "in_progress", "shipping", "satellites-intent-plan-review", "")
	if err == nil || enact {
		t.Fatalf("skill-at-checkpoint-state: enact=%v err=%v, want enact=false err!=nil", enact, err)
	}
	if !strings.Contains(err.Error(), "--checkpoint") {
		t.Fatalf("skill-at-checkpoint-state error = %q, want it to steer to --checkpoint", err)
	}
	// non-checkpoint state + --checkpoint → error; the edges-hint clause is appended
	// so a stuck agent is pointed at the real gate (sty_4300e117), never left to guess.
	enact2, err2 := checkpointDecision(true, false, "running", "", "", " — from \"running\" the governing workflow's transitions are: → complete (--skill satellites-task-report-review)")
	if err2 == nil || enact2 {
		t.Fatalf("checkpoint-at-non-checkpoint: enact=%v err=%v, want enact=false err!=nil", enact2, err2)
	}
	if !strings.Contains(err2.Error(), "satellites-task-report-review") {
		t.Fatalf("checkpoint-at-non-checkpoint error = %q, want it to name the available gate from the edges hint", err2)
	}
	// non-checkpoint state + --skill → no checkpoint, proceed to gate path.
	if enact, err := checkpointDecision(false, false, "shipping", "", "satellites-commit-push-review", ""); err != nil || enact {
		t.Fatalf("gate-elsewhere: enact=%v err=%v, want enact=false err=nil", enact, err)
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

// TestParseMaxIterations pins the KV-bound fallback safety property (sty_0c98760e):
// a clean positive integer in the variable_get response overrides the yaml
// bound, and EVERYTHING else — malformed JSON, absent/empty value, non-numeric,
// zero, or negative — yields 0 so the caller keeps the yaml value. A
// misconfigured KV must never weaken or break the fail-loop bound.
func TestParseMaxIterations(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int
	}{
		{"positive value overrides", `{"value":"5"}`, 5},
		{"whitespace trimmed", `{"value":" 7 "}`, 7},
		{"empty value falls back", `{"value":""}`, 0},
		{"missing value field falls back", `{"other":"5"}`, 0},
		{"non-numeric falls back", `{"value":"lots"}`, 0},
		{"zero falls back", `{"value":"0"}`, 0},
		{"negative falls back", `{"value":"-3"}`, 0},
		{"malformed json falls back", `not json`, 0},
		{"empty response falls back", ``, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseMaxIterations(json.RawMessage(c.raw)); got != c.want {
				t.Fatalf("parseMaxIterations(%q) = %d, want %d", c.raw, got, c.want)
			}
		})
	}
}

// TestResolveWorkflowMaxIterations_IDGuard pins that the KV lookup is skipped —
// returning 0 so the yaml bound stands — when the story carries no project or
// workspace scope to root the variable cascade. This short-circuits before any
// dispatch.
func TestResolveWorkflowMaxIterations_IDGuard(t *testing.T) {
	cases := []struct {
		name  string
		story reviewStory
	}{
		{"no project", reviewStory{WorkspaceID: "wksp_1"}},
		{"no workspace", reviewStory{ProjectID: "proj_1"}},
		{"neither", reviewStory{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveWorkflowMaxIterations(context.Background(), reviewOpts{}, c.story); got != 0 {
				t.Fatalf("resolveWorkflowMaxIterations(%+v) = %d, want 0 (yaml stands)", c.story, got)
			}
		})
	}
}
