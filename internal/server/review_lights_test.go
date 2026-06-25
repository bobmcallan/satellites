package server

import (
	"strings"
	"testing"
)

// TestBuildReviewLights pins the numbered per-stage derivation (sty_a844aa55):
// a gate is ONE numbered step, a looped gate keeps its number and shows its final
// verdict, the in-progress step is "current" only on a non-terminal story, and a
// terminal story shows no current step (the trailing-amber-on-done fix).
func TestBuildReviewLights(t *testing.T) {
	// A gate rejected twice then accepted = ONE step, final pass, looped ×2.
	looped := buildReviewLights([]reviewEvent{
		{Kind: "review_requested", Gate: "satellites-integration-review", When: "2026-06-25T01:00:00Z"},
		{Kind: "review_reject", When: "2026-06-25T01:01:00Z"},
		{Kind: "review_requested", Gate: "satellites-integration-review", When: "2026-06-25T02:00:00Z"},
		{Kind: "review_reject", When: "2026-06-25T02:01:00Z"},
		{Kind: "review_requested", Gate: "satellites-integration-review", When: "2026-06-25T03:00:00Z"},
		{Kind: "review_accept", When: "2026-06-25T03:01:00Z"},
	}, "shipping")
	if len(looped) != 1 {
		t.Fatalf("looped gate should collapse to 1 step, got %+v", looped)
	}
	if looped[0].Index != 1 || looped[0].State != "pass" || looped[0].Loops != 2 {
		t.Fatalf("looped step = %+v, want {Index:1 State:pass Loops:2}", looped[0])
	}
	if looped[0].Gate != "satellites-integration-review" {
		t.Errorf("step gate = %q, want the gate name", looped[0].Gate)
	}

	// A re-request after a pass, on a NON-terminal story → the step is "current".
	cur := buildReviewLights([]reviewEvent{
		{Kind: "review_requested", Gate: "g", When: "t1"},
		{Kind: "review_accept", When: "t2"},
		{Kind: "review_requested", Gate: "g", When: "t3"},
	}, "in_progress")
	if len(cur) != 1 || cur[0].State != "current" {
		t.Errorf("re-request (non-terminal) = %+v, want [current]", cur)
	}

	// SAME events on a DONE story → NO current; the step shows its reached pass.
	// This is the trailing-amber-on-done fix.
	done := buildReviewLights([]reviewEvent{
		{Kind: "review_requested", Gate: "g", When: "t1"},
		{Kind: "review_accept", When: "t2"},
		{Kind: "review_requested", Gate: "g", When: "t3"},
	}, "done")
	if len(done) != 1 || done[0].State != "pass" {
		t.Errorf("done story with dangling request = %+v, want [pass] (no trailing amber)", done)
	}

	// A pure dangling request (never verdicted) on a done story → no light.
	if d := buildReviewLights([]reviewEvent{{Kind: "review_requested", Gate: "g", When: "t1"}}, "done"); len(d) != 0 {
		t.Errorf("dangling request on done story should render nothing, got %+v", d)
	}

	// A clean four-gate done story → 4 numbered pass steps, in order.
	clean := buildReviewLights([]reviewEvent{
		{Kind: "review_requested", Gate: "satellites-intent-plan-review", When: "1"}, {Kind: "review_accept", When: "2"},
		{Kind: "review_requested", Gate: "satellites-integration-review", When: "3"}, {Kind: "review_accept", When: "4"},
		{Kind: "review_requested", Gate: "satellites-commit-push-review", When: "5"}, {Kind: "review_accept", When: "6"},
		{Kind: "review_requested", Gate: "satellites-implementation-summary-review", When: "7"}, {Kind: "review_accept", When: "8"},
	}, "done")
	if len(clean) != 4 {
		t.Fatalf("clean done story = %d steps, want 4 (%+v)", len(clean), clean)
	}
	for i, l := range clean {
		if l.Index != i+1 || l.State != "pass" {
			t.Errorf("clean step[%d] = %+v, want Index %d pass", i, l, i+1)
		}
	}

	// No review rows → no lights.
	if got := buildReviewLights(nil, "in_progress"); len(got) != 0 {
		t.Errorf("empty events should yield no lights, got %+v", got)
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

// TestStoryRowReviewLights is the render test: a story row emits numbered review
// circles carrying their stage number, state class, and a looped-stage title.
func TestStoryRowReviewLights(t *testing.T) {
	html := renderProjectDetail(t, projectDetailData{
		Project: projectRow{ID: "proj_x", Name: "panel"},
		Stories: []storyRow{{
			ID: "sty_x", Title: "lit story", Status: "in_progress",
			Reviews: []reviewLight{
				{Index: 1, State: "pass", Gate: "satellites-intent-plan-review"},
				{Index: 2, State: "current", Gate: "satellites-integration-review", Loops: 1},
			},
		}},
	})

	if !strings.Contains(html, `<th class="col-reviews">reviews</th>`) {
		t.Errorf("reviews column header missing:\n%s", html)
	}
	if !strings.Contains(html, `data-field="story-reviews"`) {
		t.Errorf("review lights cell missing")
	}
	for _, want := range []string{
		`review-light review-light-pass`,
		`review-light review-light-current`,
		`data-review="current"`,
		`data-step="2"`,
		`>1</span>`, // the stage number renders inside the circle
		`looped ×1`, // looped-stage title
	} {
		if !strings.Contains(html, want) {
			t.Errorf("review-light markup missing %q\n%s", want, html)
		}
	}
}
