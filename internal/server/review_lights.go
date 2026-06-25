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
// (sty_14f07f22, sty_a844aa55, sty_24609877): one per reviewer-gate VERDICT.
// Index is the gate's 1-based STAGE number (stable across a stage's retries);
// State is "pass" (solid green), "fail" (solid red), or "current" (hollow amber —
// the in-progress stage, shown only on a non-terminal story). Each attempt is its
// own light, so a stage that failed twice then passed reads ①red ①red ①green —
// the repeats are visible, sharing the stage number. Gate/When feed the title.
type reviewLight struct {
	Index int
	State string
	Gate  string
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

// buildReviewLights folds a story's chronological review_* events into the strip
// (sty_24609877): one light PER VERDICT — review_accept → "pass" (green),
// review_reject → "fail" (red) — each labelled with its gate's STAGE NUMBER,
// assigned by the gate's first appearance (so the numbers read 1,2,3,… in
// workflow order). A stage that loops keeps its number while each attempt shows
// its own colour, so the repeats are visible (e.g. ①red ①red ①green ②red ②green).
// A trailing unresolved request renders the in-progress "current" stage (hollow
// amber), but only when the story is NOT terminal — a closed story (done/
// cancelled) shows no current light (the trailing-amber-on-done fix).
func buildReviewLights(evts []reviewEvent, status string) []reviewLight {
	stage := map[string]int{}
	stageOf := func(gate string) int {
		if strings.TrimSpace(gate) == "" {
			gate = "?"
		}
		if n, ok := stage[gate]; ok {
			return n
		}
		n := len(stage) + 1
		stage[gate] = n
		return n
	}

	lights := []reviewLight{}
	pendingGate := ""
	pendingActive := false
	for _, e := range evts {
		switch e.Kind {
		case "review_requested":
			pendingGate = e.Gate
			pendingActive = true
			stageOf(e.Gate) // reserve the stage number at first sight
		case "review_accept":
			g := firstNonEmpty(e.Gate, pendingGate)
			lights = append(lights, reviewLight{Index: stageOf(g), State: "pass", Gate: g, When: e.When})
			pendingGate, pendingActive = "", false
		case "review_reject":
			g := firstNonEmpty(e.Gate, pendingGate)
			lights = append(lights, reviewLight{Index: stageOf(g), State: "fail", Gate: g, When: e.When})
			pendingGate, pendingActive = "", false
		}
	}

	// A trailing request with no verdict is the in-progress stage — shown only
	// when the story is not terminal (a closed story has no current stage).
	if pendingActive && !isTerminalReviewStatus(status) {
		lights = append(lights, reviewLight{Index: stageOf(pendingGate), State: "current", Gate: pendingGate})
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
