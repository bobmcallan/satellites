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
