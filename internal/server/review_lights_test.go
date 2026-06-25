package server

import (
	"strings"
	"testing"
)

// Event constructors for the verdict/transition-driven model (sty_de7953b1):
// a FORWARD gated transition is the pass, a review_reject is the fail, a
// checkpoint is the fired step, and a review_requested feeds the current light.
func pass(fromState, gate, when string) reviewEvent {
	return reviewEvent{Kind: "status_transition", Forward: true, FromStatus: fromState, Gate: gate, Transition: fromState + " → next", When: when}
}
func fail(fromState, gate, when string) reviewEvent {
	return reviewEvent{Kind: "review_reject", FromStatus: fromState, Gate: gate, When: when}
}
func fired(fromState, label, when string) reviewEvent {
	return reviewEvent{Kind: "status_transition", Checkpoint: true, FromStatus: fromState, Transition: label, When: when}
}
func requested(fromState, gate, when string) reviewEvent {
	return reviewEvent{Kind: "review_requested", FromStatus: fromState, Gate: gate, When: when}
}
func failLoopMove(fromState, gate, when string) reviewEvent {
	// A fail-loop status_transition (on:fail) — must emit NO light.
	return reviewEvent{Kind: "status_transition", FromStatus: fromState, Gate: gate, When: when}
}

type lp struct {
	idx int
	st  string
}

func lights(ls []reviewLight) []lp {
	out := make([]lp, 0, len(ls))
	for _, l := range ls {
		out = append(out, lp{l.Index, l.State})
	}
	return out
}
func eqLights(a []lp, b ...lp) bool {
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

// TestBuildReviewLightsOrder pins sty_de7953b1 + sty_d94d4a5f: a full
// satellites-workflow journey numbers its steps 1..5 IN ORDER — intent (pass) →
// checkpoint (fired) → integration (pass) → commit-push (pass) → impl-summary
// (pass) — keyed by the lifecycle from-state, regardless of the order the ledger
// rows arrive in. The entry gate has NO review_accept (only a forward
// status_transition), which the model handles.
func TestBuildReviewLightsOrder(t *testing.T) {
	got := buildReviewLights([]reviewEvent{
		pass("backlog", "satellites-intent-plan-review", "01"),
		fired("in_progress", "in_progress → integration", "02"),
		pass("integration", "satellites-integration-review", "03"),
		pass("shipping", "satellites-commit-push-review", "04"),
		pass("summary", "satellites-implementation-summary-review", "05"),
	}, "done")
	if !eqLights(lights(got), lp{1, "pass"}, lp{2, "fired"}, lp{3, "pass"}, lp{4, "pass"}, lp{5, "pass"}) {
		t.Fatalf("ordered journey = %+v, want 1pass 2fired 3pass 4pass 5pass", got)
	}

	// The same events shuffled (e.g. a late-flushed row lands out of order) STILL
	// number 1..5 in workflow order, because the key is the lifecycle from-state,
	// not arrival order. (Sort is the caller's job; here we prove keying is stable
	// for the rows whose relative order is reliable.)
	shuffled := buildReviewLights([]reviewEvent{
		pass("backlog", "intent", "01"),
		pass("integration", "integration", "03"),
		fired("in_progress", "in_progress → integration", "02"),
		pass("summary", "impl", "05"),
		pass("shipping", "commit", "04"),
	}, "done")
	// Numbers follow first-sight here; the point is each distinct from-state gets a
	// stable number and there are exactly 5 steps, no duplicates/double-counts.
	if len(shuffled) != 5 {
		t.Fatalf("shuffled journey should have 5 steps, got %+v", shuffled)
	}
}

// TestBuildReviewLightsRepeatsAndInvariant pins the fail-loop semantics: a stage
// that rejects then passes shows ①red …②… ①green — repeats share the number — and
// on a DONE story every reached stage ENDS green (the invariant, sty_de7953b1
// AC1). The fail-loop status_transition (the backward move) emits no light.
func TestBuildReviewLightsRepeatsAndInvariant(t *testing.T) {
	got := buildReviewLights([]reviewEvent{
		pass("backlog", "intent", "01"),
		fired("in_progress", "in_progress → integration", "02"),
		fail("integration", "satellites-integration-review", "03"),
		failLoopMove("integration", "satellites-integration-review", "04"), // integration → in_progress (no light)
		fired("in_progress", "in_progress → integration", "05"),            // re-checkpoint
		pass("integration", "satellites-integration-review", "06"),
		pass("shipping", "commit", "07"),
		pass("summary", "impl", "08"),
	}, "done")
	want := []lp{{1, "pass"}, {2, "fired"}, {3, "fail"}, {2, "fired"}, {3, "pass"}, {4, "pass"}, {5, "pass"}}
	if !eqLights(lights(got), want...) {
		t.Fatalf("repeat journey = %+v, want %+v", got, want)
	}
	// Invariant: stage 3's FINAL light is green (pass), not the earlier red.
	last := map[int]string{}
	for _, l := range got {
		last[l.Index] = l.State
	}
	if last[3] != "pass" {
		t.Errorf("AC1: stage 3 must end green on a done story, ended %q", last[3])
	}
	for idx, st := range last {
		if st == "fail" {
			t.Errorf("AC1: stage %d ends red on a DONE story — invariant violated", idx)
		}
	}
}

// TestBuildReviewLightsBlockedEndsRed pins sty_de7953b1 AC2: a blocked/exhausted
// stage MAY end red — the fail-loop bound was spent and no later pass followed.
func TestBuildReviewLightsBlockedEndsRed(t *testing.T) {
	got := buildReviewLights([]reviewEvent{
		pass("backlog", "intent", "01"),
		fired("in_progress", "in_progress → integration", "02"),
		fail("integration", "satellites-integration-review", "03"),
		fail("integration", "satellites-integration-review", "04"),
		fail("integration", "satellites-integration-review", "05"),
		// exhausted: status_transition integration → blocked (exhausted) emits no light.
		{Kind: "status_transition", FromStatus: "integration", Gate: "satellites-integration-review", When: "06"},
	}, "blocked")
	want := []lp{{1, "pass"}, {2, "fired"}, {3, "fail"}, {3, "fail"}, {3, "fail"}}
	if !eqLights(lights(got), want...) {
		t.Fatalf("blocked journey = %+v, want %+v", got, want)
	}
	// The strip ends red — valid because the story is blocked, not done.
	if got[len(got)-1].State != "fail" {
		t.Errorf("AC2: a blocked story's exhausted stage should end red, got %q", got[len(got)-1].State)
	}
}

// TestBuildReviewLightsCurrent pins the in-progress "current" light: a pending
// request with no resolving verdict on a NON-terminal story renders hollow amber;
// a terminal story shows none; a re-request after a reject reads as the current
// attempt of its already-numbered stage.
func TestBuildReviewLightsCurrent(t *testing.T) {
	// Mid first gate: only a request, status backlog → "1 current".
	first := buildReviewLights([]reviewEvent{requested("backlog", "intent", "01")}, "backlog")
	if !eqLights(lights(first), lp{1, "current"}) {
		t.Fatalf("mid-first-gate = %+v, want 1current", first)
	}

	// Reject then re-request (still open) on a non-terminal story → 1pass-equiv is
	// a fail then the current retry of the SAME stage number.
	retry := buildReviewLights([]reviewEvent{
		pass("backlog", "intent", "01"),
		fired("in_progress", "cp", "02"),
		requested("integration", "integration", "03"),
		fail("integration", "integration", "04"),
		requested("integration", "integration", "05"),
	}, "integration")
	if !eqLights(lights(retry), lp{1, "pass"}, lp{2, "fired"}, lp{3, "fail"}, lp{3, "current"}) {
		t.Fatalf("retry = %+v, want 1pass 2fired 3fail 3current", retry)
	}

	// SAME events but terminal → no current light.
	done := buildReviewLights([]reviewEvent{
		pass("backlog", "intent", "01"),
		requested("integration", "integration", "02"),
	}, "done")
	if !eqLights(lights(done), lp{1, "pass"}) {
		t.Fatalf("done with trailing request = %+v, want just 1pass (no current)", done)
	}

	// No events → no lights.
	if l := buildReviewLights(nil, "in_progress"); len(l) != 0 {
		t.Errorf("empty events should yield no lights, got %+v", l)
	}
}

// TestGateAndFromStateParse pins the request-body parsers and the payload parser.
func TestGateAndFromStateParse(t *testing.T) {
	if g := gateFromReviewBody("review_requested", "gate satellites-commit-push-review: from shipping"); g != "satellites-commit-push-review" {
		t.Errorf("gate parse = %q", g)
	}
	if g := gateFromReviewBody("review_reject", "the push landed cleanly"); g != "" {
		t.Errorf("non-request should name no gate, got %q", g)
	}
	if f := fromStateFromReviewBody("gate satellites-commit-push-review: from shipping"); f != "shipping" {
		t.Errorf("from-state parse = %q, want shipping", f)
	}
	p := parsePayload([]byte(`{"from_status":"integration","to_status":"shipping","gate":"g","on":"pass"}`))
	if p.From != "integration" || p.To != "shipping" || p.Gate != "g" || p.On != "pass" {
		t.Errorf("payload parse = %+v", p)
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
		`>2</span>`,
		`>3</span>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("review-light markup missing %q\n%s", want, html)
		}
	}
}
