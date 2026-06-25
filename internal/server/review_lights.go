package server

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/verb"
)

// reviewLight is one numbered circle in a story row's review-history strip
// (sty_14f07f22, renumbered sty_a844aa55): one per reviewer-gate STAGE, in
// workflow order. Index is its 1-based stage number; a gate that LOOPED keeps the
// same number. State is "pass" (solid green), "fail" (solid red), or "current"
// (hollow amber — the in-progress stage, shown only on a non-terminal story).
// Loops is the extra attempts beyond the first; Gate/When feed the hover title.
type reviewLight struct {
	Index int
	State string
	Gate  string
	Loops int
	When  string
}

// reviewEvent is the transport-neutral projection of a review_* ledger row that
// buildReviewLights folds into the strip — kept separate so the derivation is
// unit-testable without the ledger.
type reviewEvent struct {
	Kind string // review_requested | review_accept | review_reject
	Gate string // gate name (parsed from a review_requested body), else ""
	When string // RFC3339
}

// buildReviewLights folds a story's chronological review_* events into a NUMBERED
// per-STAGE strip (sty_a844aa55): each reviewer gate is one numbered step, and a
// gate that LOOPED (rejected then re-run, or several attempts) keeps the SAME
// number, showing its FINAL verdict. A completed step is "pass" (solid green) or
// "fail" (solid red); the in-progress step — a gate whose latest event is an
// unresolved request — is "current" (hollow amber), rendered only when the story
// is not terminal. A terminal story (done/cancelled) shows no current step:
// a dangling unresolved request is ignored, which fixes the stale trailing-amber
// light on a closed story (the verdict it already reached shows instead).
func buildReviewLights(evts []reviewEvent, status string) []reviewLight {
	type step struct {
		gate        string
		lastVerdict string // "pass" | "fail" | ""
		pending     bool   // a request awaits a verdict
		attempts    int    // resolved verdicts for this gate
		when        string
	}
	idx := map[string]int{}
	steps := []*step{}
	get := func(gate string) *step {
		if strings.TrimSpace(gate) == "" {
			gate = "?"
		}
		if i, ok := idx[gate]; ok {
			return steps[i]
		}
		idx[gate] = len(steps)
		s := &step{gate: gate}
		steps = append(steps, s)
		return s
	}
	pendingGate := ""
	for _, e := range evts {
		switch e.Kind {
		case "review_requested":
			pendingGate = e.Gate
			s := get(e.Gate)
			s.pending, s.when = true, e.When
		case "review_accept":
			s := get(firstNonEmpty(e.Gate, pendingGate))
			s.lastVerdict, s.pending, s.attempts, s.when = "pass", false, s.attempts+1, e.When
			pendingGate = ""
		case "review_reject":
			s := get(firstNonEmpty(e.Gate, pendingGate))
			s.lastVerdict, s.pending, s.attempts, s.when = "fail", false, s.attempts+1, e.When
			pendingGate = ""
		}
	}

	terminal := isTerminalReviewStatus(status)
	lights := make([]reviewLight, 0, len(steps))
	for _, s := range steps {
		var state string
		switch {
		case s.pending && !terminal:
			state = "current"
		case s.lastVerdict != "":
			state = s.lastVerdict
		case terminal:
			continue // a dangling request on a closed story renders nothing
		default:
			state = "current"
		}
		loops := s.attempts
		if state != "current" && loops > 0 {
			loops-- // a resolved step's first attempt is not a loop
		}
		lights = append(lights, reviewLight{Index: len(lights) + 1, State: state, Gate: s.gate, Loops: loops, When: s.when})
	}
	return lights
}

// isTerminalReviewStatus reports the canonical terminal states for the lights
// derivation — a story here is closed when done or cancelled.
func isTerminalReviewStatus(status string) bool {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case "done", "cancelled", "canceled":
		return true
	}
	return false
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// gateFromReviewBody pulls the gate name out of a review_requested body, whose
// shape is "gate <name>: from <state>". Verdict rows (accept/reject) carry the
// gate's free-text notes, not a gate name, so they return "" and inherit the
// preceding request's gate in buildReviewLights.
func gateFromReviewBody(kind, body string) string {
	if kind != "review_requested" {
		return ""
	}
	b := strings.TrimSpace(body)
	const pfx = "gate "
	if !strings.HasPrefix(b, pfx) {
		return ""
	}
	b = b[len(pfx):]
	if i := strings.IndexByte(b, ':'); i >= 0 {
		b = b[:i]
	}
	return strings.TrimSpace(b)
}

// latestReviewEvents fetches the project's review_* ledger rows in one batched,
// paginated ledger_list (kind_prefix "review_") and returns the chronological
// event projection per story_id — one ledger query for the whole list render,
// bucketed by story (mirrors latestEngagements). Read-only. The per-story
// derivation (buildReviewLights) runs in attachReviewLights, where each story's
// status is in hand (the strip is status-aware: sty_a844aa55).
func latestReviewEvents(ctx context.Context, projectID string) (map[string][]reviewEvent, error) {
	byStory := map[string][]reviewEvent{}
	cursor := ""
	for {
		req, err := json.Marshal(verb.LedgerListRequest{
			ProjectID:  projectID,
			KindPrefix: "review_",
			Limit:      2000,
			Cursor:     cursor,
		})
		if err != nil {
			return nil, err
		}
		raw, err := verb.Dispatch(ctx, "ledger_list", req)
		if err != nil {
			return nil, err
		}
		var resp verb.LedgerListResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, err
		}
		for _, e := range resp.Entries {
			if e.StoryID == "" {
				continue
			}
			byStory[e.StoryID] = append(byStory[e.StoryID], reviewEvent{
				Kind: e.Kind,
				Gate: gateFromReviewBody(e.Kind, e.Body),
				When: e.CreatedAt.UTC().Format(time.RFC3339),
			})
		}
		if resp.NextCursor == "" {
			break
		}
		cursor = resp.NextCursor
	}
	for sid := range byStory {
		evts := byStory[sid]
		// ledger_list returns oldest-first; sort defensively so the strip is
		// chronological even across pages.
		sort.SliceStable(evts, func(i, j int) bool { return evts[i].When < evts[j].When })
		byStory[sid] = evts
	}
	return byStory, nil
}

// attachReviewLights decorates the story rows with their numbered review-history
// strip (sty_14f07f22, sty_a844aa55), via one batched ledger read for the
// project. The derivation is status-aware (a terminal story shows no current
// step). A failed read degrades to no lights, never a page error.
func attachReviewLights(ctx context.Context, projectID string, stories []storyRow) {
	byStory, err := latestReviewEvents(ctx, projectID)
	if err != nil {
		arbor.WarnCtx(ctx, "project_detail: review lights", "id", projectID, "err", err)
		return
	}
	for i := range stories {
		if evts, ok := byStory[stories[i].ID]; ok {
			stories[i].Reviews = buildReviewLights(evts, stories[i].Status)
		}
	}
}
