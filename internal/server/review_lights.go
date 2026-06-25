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
// (sty_14f07f22, sty_a844aa55, sty_24609877, sty_d94d4a5f): one per workflow STEP
// — a reviewer-gate VERDICT or an ungated checkpoint that fired. Index is the
// step's 1-based STAGE number (stable across a stage's retries); State is "pass"
// (solid green), "fail" (solid red), "fired" (solid neutral — an ungated
// checkpoint that advanced), or "current" (hollow amber — the in-progress stage,
// shown only on a non-terminal story). Each attempt is its own light, so a stage
// that failed twice then passed reads ①red ①red ①green — the repeats are visible,
// sharing the stage number. Gate/When feed the title.
type reviewLight struct {
	Index int
	State string
	Gate  string
	When  string
}

// reviewEvent is the transport-neutral projection of a review_* or
// status_transition ledger row that buildReviewLights folds into the strip — kept
// separate so the derivation is unit-testable without the ledger.
type reviewEvent struct {
	Kind       string // review_requested | review_accept | review_reject | status_transition
	Gate       string // gate name (parsed from a review_requested body), else ""
	Transition string // "from → to" for a status_transition row, else ""
	Checkpoint bool   // true when a status_transition row is an ungated checkpoint (trigger:checkpoint)
	When       string // RFC3339Nano
}

// buildReviewLights folds a story's chronological events into the strip
// (sty_24609877, sty_d94d4a5f): one light PER WORKFLOW STEP. A reviewer gate
// contributes one light per VERDICT — review_accept → "pass" (green),
// review_reject → "fail" (red); an UNGATED CHECKPOINT (a status_transition row
// with trigger:checkpoint, which carries no review verdict) contributes a "fired"
// light. Each step is labelled with its STAGE NUMBER, assigned by first
// appearance in chronological order (so the numbers read 1,2,3,… in workflow
// order, the checkpoint slotting in between its neighbouring gates). A gate that
// loops keeps its number while each attempt shows its own colour, so the repeats
// are visible (e.g. ①red ①red ①green ②red ②green). Gate-DRIVEN status_transition
// rows are ignored — their move is already represented by the verdict light, so
// counting them too would double up. A trailing unresolved request renders the
// in-progress "current" stage (hollow amber), but only when the story is NOT
// terminal — a closed story (done/cancelled) shows no current light.
func buildReviewLights(evts []reviewEvent, status string) []reviewLight {
	stage := map[string]int{}
	stageOf := func(key string) int {
		if strings.TrimSpace(key) == "" {
			key = "?"
		}
		if n, ok := stage[key]; ok {
			return n
		}
		n := len(stage) + 1
		stage[key] = n
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
		case "status_transition":
			// Only an UNGATED checkpoint becomes its own light — a gate-driven
			// transition is already counted by its verdict above.
			if !e.Checkpoint {
				continue
			}
			key := strings.TrimSpace(e.Transition)
			if key == "" {
				key = "checkpoint"
			}
			lights = append(lights, reviewLight{Index: stageOf(key), State: "fired", Gate: e.Transition, When: e.When})
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

// latestReviewEvents fetches the project's review_* AND status_transition ledger
// rows and returns the chronological event projection per story_id — so the strip
// can show ungated checkpoint steps alongside gate verdicts (sty_d94d4a5f). Two
// batched, paginated ledger_list reads (kind_prefix "review_", kind
// "status_transition") bucketed by story (mirrors latestEngagements). Read-only.
// The per-story derivation (buildReviewLights) runs in attachReviewLights, where
// each story's status is in hand (the strip is status-aware: sty_a844aa55).
func latestReviewEvents(ctx context.Context, projectID string) (map[string][]reviewEvent, error) {
	byStory := map[string][]reviewEvent{}

	collect := func(req verb.LedgerListRequest, project func(lightRow) reviewEvent) error {
		cursor := ""
		for {
			req.Cursor = cursor
			req.Limit = 2000
			body, err := json.Marshal(req)
			if err != nil {
				return err
			}
			raw, err := verb.Dispatch(ctx, "ledger_list", body)
			if err != nil {
				return err
			}
			var resp verb.LedgerListResponse
			if err := json.Unmarshal(raw, &resp); err != nil {
				return err
			}
			for _, e := range resp.Entries {
				if e.StoryID == "" {
					continue
				}
				byStory[e.StoryID] = append(byStory[e.StoryID], project(lightRow{
					Kind: e.Kind, Body: e.Body, Payload: e.Payload, When: e.CreatedAt,
				}))
			}
			if resp.NextCursor == "" {
				return nil
			}
			cursor = resp.NextCursor
		}
	}

	if err := collect(
		verb.LedgerListRequest{ProjectID: projectID, KindPrefix: "review_"},
		func(e lightRow) reviewEvent {
			return reviewEvent{
				Kind: e.Kind,
				Gate: gateFromReviewBody(e.Kind, e.Body),
				When: e.When.UTC().Format(time.RFC3339Nano),
			}
		},
	); err != nil {
		return nil, err
	}
	if err := collect(
		verb.LedgerListRequest{ProjectID: projectID, Kind: "status_transition"},
		func(e lightRow) reviewEvent {
			from, to, checkpoint := parseStatusTransition(e.Payload, e.Body)
			return reviewEvent{
				Kind:       "status_transition",
				Transition: transitionLabel(from, to, e.Body),
				Checkpoint: checkpoint,
				When:       e.When.UTC().Format(time.RFC3339Nano),
			}
		},
	); err != nil {
		return nil, err
	}

	for sid := range byStory {
		evts := byStory[sid]
		// ledger_list returns oldest-first; the two reads interleave, so sort by
		// the (nano-precision) timestamp to recover a single chronological order
		// — the stage numbering depends on it.
		sort.SliceStable(evts, func(i, j int) bool { return evts[i].When < evts[j].When })
		byStory[sid] = evts
	}
	return byStory, nil
}

// lightRow is the minimal slice of a ledger row the projections read.
type lightRow struct {
	Kind    string
	Body    string
	Payload json.RawMessage
	When    time.Time
}

// parseStatusTransition reads from/to and the checkpoint marker from a
// status_transition row. The payload (trigger:checkpoint) is authoritative; the
// body ("from → to (checkpoint)") is the fallback when the payload is absent.
func parseStatusTransition(payload json.RawMessage, body string) (from, to string, checkpoint bool) {
	var p struct {
		From    string `json:"from_status"`
		To      string `json:"to_status"`
		Trigger string `json:"trigger"`
	}
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &p)
	}
	from = strings.TrimSpace(p.From)
	to = strings.TrimSpace(p.To)
	checkpoint = p.Trigger == "checkpoint" || strings.Contains(body, "(checkpoint)")
	return from, to, checkpoint
}

// transitionLabel renders the human "from → to" used as a checkpoint light's
// title and stage key. Falls back to the row body (minus the checkpoint marker)
// when the payload carried no statuses.
func transitionLabel(from, to, body string) string {
	if from != "" && to != "" {
		return from + " → " + to
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(body), "(checkpoint)"))
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
