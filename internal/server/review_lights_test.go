package server

import (
	"strings"
	"testing"
)

// TestBuildReviewLights pins the ledger→lights derivation (sty_14f07f22): one
// light per verdict in order (accept→pass, reject→fail), a repeated gate adds a
// light per run, and a trailing unresolved request yields a single pending light.
func TestBuildReviewLights(t *testing.T) {
	// A gate rejected twice then accepted, each request naming its gate.
	evts := []reviewEvent{
		{Kind: "review_requested", Gate: "satellites-integration-review", When: "2026-06-25T01:00:00Z"},
		{Kind: "review_reject", When: "2026-06-25T01:01:00Z"},
		{Kind: "review_requested", Gate: "satellites-integration-review", When: "2026-06-25T02:00:00Z"},
		{Kind: "review_reject", When: "2026-06-25T02:01:00Z"},
		{Kind: "review_requested", Gate: "satellites-integration-review", When: "2026-06-25T03:00:00Z"},
		{Kind: "review_accept", When: "2026-06-25T03:01:00Z"},
	}
	got := buildReviewLights(evts)
	want := []string{"fail", "fail", "pass"}
	if len(got) != len(want) {
		t.Fatalf("light count = %d, want %d (%+v)", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].State != w {
			t.Errorf("light[%d].State = %q, want %q", i, got[i].State, w)
		}
	}
	// The verdict inherits the preceding request's gate (the verdict row names none).
	if got[2].Gate != "satellites-integration-review" {
		t.Errorf("verdict gate = %q, want the preceding request's gate", got[2].Gate)
	}

	// A trailing request with no verdict → a single pending light after the verdicts.
	pend := buildReviewLights([]reviewEvent{
		{Kind: "review_requested", Gate: "g", When: "t1"},
		{Kind: "review_accept", When: "t2"},
		{Kind: "review_requested", Gate: "g", When: "t3"},
	})
	if len(pend) != 2 || pend[0].State != "pass" || pend[1].State != "pending" {
		t.Errorf("pending derivation = %+v, want [pass pending]", pend)
	}

	// No review rows → no lights.
	if got := buildReviewLights(nil); len(got) != 0 {
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

// TestStoryRowReviewLights is the render test (AC1/AC2): a story row emits a
// reviews column whose lights carry the per-verdict state classes, and the
// stories table carries the reviews header.
func TestStoryRowReviewLights(t *testing.T) {
	html := renderProjectDetail(t, projectDetailData{
		Project: projectRow{ID: "proj_x", Name: "panel"},
		Stories: []storyRow{{
			ID: "sty_x", Title: "lit story", Status: "in_progress",
			Reviews: []reviewLight{
				{State: "fail", Gate: "satellites-integration-review"},
				{State: "pass", Gate: "satellites-integration-review"},
				{State: "pending", Gate: "satellites-commit-push-review"},
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
		`review-light review-light-fail`,
		`review-light review-light-pass`,
		`review-light review-light-pending`,
		`data-review="pending"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("review-light markup missing %q", want)
		}
	}
}
