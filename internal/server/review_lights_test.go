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
// circles carrying their stage number and state class, with repeats sharing a
// number.
func TestStoryRowReviewLights(t *testing.T) {
	html := renderProjectDetail(t, projectDetailData{
		Project: projectRow{ID: "proj_x", Name: "panel"},
		Stories: []storyRow{{
			ID: "sty_x", Title: "lit story", Status: "in_progress",
			Reviews: []reviewLight{
				{Index: 1, State: "fail", Gate: "satellites-integration-review"},
				{Index: 1, State: "pass", Gate: "satellites-integration-review"},
				{Index: 2, State: "current", Gate: "satellites-commit-push-review"},
			},
		}},
	})

	if !strings.Contains(html, `<th class="col-reviews">reviews</th>`) {
		t.Errorf("reviews column header missing:\n%s", html)
	}
	for _, want := range []string{
		`review-light review-light-fail`,
		`review-light review-light-pass`,
		`review-light review-light-current`,
		`data-review="current"`,
		`data-step="2"`,
		`>1</span>`, // the stage number renders inside the circle (repeats share it)
		`>2</span>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("review-light markup missing %q\n%s", want, html)
		}
	}
}
