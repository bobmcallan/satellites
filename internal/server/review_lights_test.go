package server

import (
	"strings"
	"testing"
)

// TestBuildReviewLights pins the per-verdict, stage-numbered derivation
// (sty_24609877): one light per verdict, each labelled with its gate's stage
// number; a stage's retries repeat the number with their own colour; the
// in-progress stage is "current" only on a non-terminal story.
func TestBuildReviewLights(t *testing.T) {
	type lp struct {
		idx int
		st  string
	}
	got := func(ls []reviewLight) []lp {
		out := make([]lp, 0, len(ls))
		for _, l := range ls {
			out = append(out, lp{l.Index, l.State})
		}
		return out
	}
	eq := func(a []lp, b ...lp) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}

	// Reference (sty_6bf2bedb): stage 1 fails twice then passes, stage 2 fails
	// once then passes → ①red ①red ①green ②red ②green.
	twoStage := buildReviewLights([]reviewEvent{
		{Kind: "review_requested", Gate: "g1", When: "1"}, {Kind: "review_reject", When: "2"},
		{Kind: "review_requested", Gate: "g1", When: "3"}, {Kind: "review_reject", When: "4"},
		{Kind: "review_requested", Gate: "g1", When: "5"}, {Kind: "review_accept", When: "6"},
		{Kind: "review_requested", Gate: "g2", When: "7"}, {Kind: "review_reject", When: "8"},
		{Kind: "review_requested", Gate: "g2", When: "9"}, {Kind: "review_accept", When: "10"},
	}, "shipping")
	if !eq(got(twoStage), lp{1, "fail"}, lp{1, "fail"}, lp{1, "pass"}, lp{2, "fail"}, lp{2, "pass"}) {
		t.Fatalf("two-stage repeats = %+v, want 1fail 1fail 1pass 2fail 2pass", twoStage)
	}

	// Re-request after a pass on a NON-terminal story → trailing current.
	cur := buildReviewLights([]reviewEvent{
		{Kind: "review_requested", Gate: "g", When: "1"}, {Kind: "review_accept", When: "2"},
		{Kind: "review_requested", Gate: "g", When: "3"},
	}, "in_progress")
	if !eq(got(cur), lp{1, "pass"}, lp{1, "current"}) {
		t.Errorf("re-request (non-terminal) = %+v, want 1pass 1current", cur)
	}

	// SAME events on a DONE story → no current light (trailing-amber fix).
	done := buildReviewLights([]reviewEvent{
		{Kind: "review_requested", Gate: "g", When: "1"}, {Kind: "review_accept", When: "2"},
		{Kind: "review_requested", Gate: "g", When: "3"},
	}, "done")
	if !eq(got(done), lp{1, "pass"}) {
		t.Errorf("done story = %+v, want just 1pass (no trailing amber)", done)
	}

	// A clean four-gate done story → one pass per stage, numbered 1..4.
	clean := buildReviewLights([]reviewEvent{
		{Kind: "review_requested", Gate: "a", When: "1"}, {Kind: "review_accept", When: "2"},
		{Kind: "review_requested", Gate: "b", When: "3"}, {Kind: "review_accept", When: "4"},
		{Kind: "review_requested", Gate: "c", When: "5"}, {Kind: "review_accept", When: "6"},
		{Kind: "review_requested", Gate: "d", When: "7"}, {Kind: "review_accept", When: "8"},
	}, "done")
	if !eq(got(clean), lp{1, "pass"}, lp{2, "pass"}, lp{3, "pass"}, lp{4, "pass"}) {
		t.Errorf("clean done = %+v, want 1..4 pass", clean)
	}

	// No review rows → no lights.
	if l := buildReviewLights(nil, "in_progress"); len(l) != 0 {
		t.Errorf("empty events should yield no lights, got %+v", l)
	}
}

// TestBuildReviewLightsCheckpoints pins sty_d94d4a5f: a story driven through
// satellites-workflow shows a light for EACH step — the four gate verdicts AND
// the ungated in_progress→integration checkpoint — numbered 1..5 in workflow
// order, with the gate-driven status_transition rows NOT double-counted.
func TestBuildReviewLightsCheckpoints(t *testing.T) {
	type lp struct {
		idx int
		st  string
	}
	got := func(ls []reviewLight) []lp {
		out := make([]lp, 0, len(ls))
		for _, l := range ls {
			out = append(out, lp{l.Index, l.State})
		}
		return out
	}
	eq := func(a []lp, b ...lp) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}

	// A full satellites-workflow journey to done: intent-plan (gate) → checkpoint
	// (ungated) → integration (gate) → commit-push (gate) → impl-summary (gate).
	// The gate-driven status_transition rows (backlog→in_progress, integration→
	// shipping, …) are interleaved and must be ignored — only the checkpoint
	// becomes its own light.
	full := buildReviewLights([]reviewEvent{
		{Kind: "review_requested", Gate: "satellites-intent-plan-review", When: "01"},
		{Kind: "review_accept", When: "02"},
		{Kind: "status_transition", Transition: "backlog → in_progress", Checkpoint: false, When: "03"},
		{Kind: "status_transition", Transition: "in_progress → integration", Checkpoint: true, When: "04"},
		{Kind: "review_requested", Gate: "satellites-integration-review", When: "05"},
		{Kind: "review_accept", When: "06"},
		{Kind: "status_transition", Transition: "integration → shipping", Checkpoint: false, When: "07"},
		{Kind: "review_requested", Gate: "satellites-commit-push-review", When: "08"},
		{Kind: "review_accept", When: "09"},
		{Kind: "status_transition", Transition: "shipping → summary", Checkpoint: false, When: "10"},
		{Kind: "review_requested", Gate: "satellites-implementation-summary-review", When: "11"},
		{Kind: "review_accept", When: "12"},
		{Kind: "status_transition", Transition: "summary → done", Checkpoint: false, When: "13"},
	}, "done")
	if !eq(got(full),
		lp{1, "pass"}, lp{2, "fired"}, lp{3, "pass"}, lp{4, "pass"}, lp{5, "pass"}) {
		t.Fatalf("full journey = %+v, want 1pass 2fired 3pass 4pass 5pass", full)
	}

	// The checkpoint can fire more than once (integration-review rejected, looped
	// back to in_progress, re-checkpointed). The repeat shares the checkpoint's
	// stage number, like a gate's retries.
	loop := buildReviewLights([]reviewEvent{
		{Kind: "review_requested", Gate: "satellites-intent-plan-review", When: "01"},
		{Kind: "review_accept", When: "02"},
		{Kind: "status_transition", Transition: "in_progress → integration", Checkpoint: true, When: "03"},
		{Kind: "review_requested", Gate: "satellites-integration-review", When: "04"},
		{Kind: "review_reject", When: "05"},
		{Kind: "status_transition", Transition: "integration → in_progress", Checkpoint: false, When: "06"},
		{Kind: "status_transition", Transition: "in_progress → integration", Checkpoint: true, When: "07"},
		{Kind: "review_requested", Gate: "satellites-integration-review", When: "08"},
		{Kind: "review_accept", When: "09"},
	}, "shipping")
	if !eq(got(loop),
		lp{1, "pass"}, lp{2, "fired"}, lp{3, "fail"}, lp{2, "fired"}, lp{3, "pass"}) {
		t.Fatalf("checkpoint loop = %+v, want 1pass 2fired 3fail 2fired 3pass", loop)
	}
}

// TestGateFromReviewBody pins the gate-name parse: only a review_requested body
// ("gate <name>: from <state>") names a gate; verdict rows return "".
func TestGateFromReviewBody(t *testing.T) {
	if g := gateFromReviewBody("review_requested", "gate satellites-commit-push-review: from shipping"); g != "satellites-commit-push-review" {
		t.Errorf("gate parse = %q", g)
	}
	if g := gateFromReviewBody("review_accept", "the push landed cleanly"); g != "" {
		t.Errorf("verdict body should name no gate, got %q", g)
	}
}

// TestParseStatusTransition pins the checkpoint detection + label: the payload
// trigger is authoritative, the body "(checkpoint)" marker is the fallback, and
// a gate-driven transition is not a checkpoint.
func TestParseStatusTransition(t *testing.T) {
	from, to, cp := parseStatusTransition([]byte(`{"from_status":"in_progress","to_status":"integration","trigger":"checkpoint"}`), "in_progress → integration (checkpoint)")
	if from != "in_progress" || to != "integration" || !cp {
		t.Errorf("payload checkpoint = (%q,%q,%v), want (in_progress,integration,true)", from, to, cp)
	}
	if got := transitionLabel(from, to, ""); got != "in_progress → integration" {
		t.Errorf("transitionLabel = %q", got)
	}
	// Gate-driven move: no checkpoint trigger.
	_, _, cp2 := parseStatusTransition([]byte(`{"from_status":"integration","to_status":"shipping"}`), "integration → shipping")
	if cp2 {
		t.Errorf("gate-driven transition should not be a checkpoint")
	}
	// Body-only fallback (payload absent) still detects the checkpoint + label.
	bf, bt, bcp := parseStatusTransition(nil, "in_progress → integration (checkpoint)")
	if !bcp {
		t.Errorf("body-marker checkpoint not detected")
	}
	if got := transitionLabel(bf, bt, "in_progress → integration (checkpoint)"); got != "in_progress → integration" {
		t.Errorf("body fallback label = %q", got)
	}
}

// TestStoryRowReviewLights is the render test: a story row emits numbered review
// circles carrying their stage number and state class, with repeats sharing a
// number; an ungated checkpoint renders as a "fired" light (sty_d94d4a5f).
func TestStoryRowReviewLights(t *testing.T) {
	html := renderProjectDetail(t, projectDetailData{
		Project: projectRow{ID: "proj_x", Name: "panel"},
		Stories: []storyRow{{
			ID: "sty_x", Title: "lit story", Status: "in_progress",
			Reviews: []reviewLight{
				{Index: 1, State: "pass", Gate: "satellites-intent-plan-review"},
				{Index: 2, State: "fired", Gate: "in_progress → integration"},
				{Index: 3, State: "fail", Gate: "satellites-integration-review"},
				{Index: 3, State: "pass", Gate: "satellites-integration-review"},
				{Index: 4, State: "current", Gate: "satellites-commit-push-review"},
			},
		}},
	})

	if !strings.Contains(html, `<th class="col-reviews">reviews</th>`) {
		t.Errorf("reviews column header missing:\n%s", html)
	}
	for _, want := range []string{
		`review-light review-light-pass`,
		`review-light review-light-fired`,
		`review-light review-light-fail`,
		`review-light review-light-current`,
		`data-review="fired"`,
		`data-step="2"`,
		`data-step="4"`,
		`>2</span>`, // the checkpoint's stage number renders inside its circle
		`>3</span>`, // the gate retries share their number
	} {
		if !strings.Contains(html, want) {
			t.Errorf("review-light markup missing %q\n%s", want, html)
		}
	}
}
