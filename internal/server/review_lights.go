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
//
// The derivation is driven by the rows that carry a RELIABLE timestamp and a
// self-describing payload — the forward gated status_transition (a gate passed),
// the review_reject (a gate failed), and the ungated checkpoint — NOT by the
// request→verdict adjacency that the earlier model assumed (sty_de7953b1). That
// model broke because `review_requested` is buffered through the local inbox and
// FLUSHED to the ledger late, so its timestamp lands out of order, and because the
// entry gate records its pass as a status_transition with no `review_accept` at
// all — so accepts could be mis-attributed to the wrong stage. Every forward gated
// transition and every reject carries its own `gate` + `from_status` in the
// payload, so each light is attributed and ordered independently.
type reviewEvent struct {
	Kind       string // review_requested | review_reject | status_transition (review_accept is redundant with the forward status_transition and is ignored)
	Gate       string // gate skill — payload `gate` for verdict/transition rows, parsed from the body for requests
	FromStatus string // from_status — the lifecycle-stable STAGE KEY (payload for verdict/transition rows, parsed from the request body)
	Transition string // "from → to" label for a checkpoint, else ""
	Checkpoint bool   // ungated checkpoint (status_transition with trigger:checkpoint)
	Forward    bool   // a gate-driven FORWARD status_transition (the gate PASSED) — emit a "pass" light
	When       string // RFC3339Nano
}

// buildReviewLights folds a story's chronological events into the strip
// (sty_24609877, sty_d94d4a5f, sty_de7953b1): one light PER WORKFLOW STEP, keyed
// by the step's lifecycle FROM-state so the numbers read 1,2,3,… in workflow order
// regardless of the order the ledger rows arrive in.
//
//   - a FORWARD gated status_transition (the gate passed) → "pass" (green);
//   - a review_reject (the gate failed and the edge loops) → "fail" (red);
//   - an ungated checkpoint → "fired" (solid neutral).
//
// review_accept rows are ignored (the forward status_transition already records
// the pass, and the entry gate has no accept at all). A stage that loops keeps its
// number while each attempt shows its own colour, so the repeats are visible (e.g.
// ①red ①green). On a non-terminal story a still-open gate (a request with no
// resolving verdict/transition) renders the in-progress "current" stage (hollow
// amber); a terminal story shows none.
//
// INVARIANT (sty_de7953b1): on a DONE story every reached stage ends GREEN — a
// reject is always followed by that stage's later forward pass, so the stage's
// FINAL light is the pass. A trailing red is valid only on a blocked/exhausted
// story, where the fail-loop bound was spent and no later pass followed.
func buildReviewLights(evts []reviewEvent, status string, liveEngagement bool) []reviewLight {
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
	// The stage key is the step's from-state (always present in a new payload);
	// fall back to the gate name, then the transition label, for older rows.
	keyOf := func(e reviewEvent) string {
		if k := strings.TrimSpace(e.FromStatus); k != "" {
			return k
		}
		if k := strings.TrimSpace(e.Gate); k != "" {
			return k
		}
		return strings.TrimSpace(e.Transition)
	}

	lights := []reviewLight{}
	reqByKey := map[string]int{}      // requests seen per stage key
	resolvedByKey := map[string]int{} // verdicts (pass/fail) seen per stage key
	type pendingReq struct{ key, gate string }
	reqOrder := []pendingReq{}

	for _, e := range evts {
		switch e.Kind {
		case "review_requested":
			k := keyOf(e)
			reqByKey[k]++
			reqOrder = append(reqOrder, pendingReq{key: k, gate: e.Gate})
		case "review_reject":
			k := keyOf(e)
			resolvedByKey[k]++
			lights = append(lights, reviewLight{Index: stageOf(k), State: "fail", Gate: e.Gate, When: e.When})
		case "status_transition":
			switch {
			case e.Checkpoint:
				k := keyOf(e)
				lights = append(lights, reviewLight{Index: stageOf(k), State: "fired", Gate: e.Transition, When: e.When})
			case e.Forward:
				k := keyOf(e)
				resolvedByKey[k]++
				lights = append(lights, reviewLight{Index: stageOf(k), State: "pass", Gate: e.Gate, When: e.When})
			}
			// A backward/fail-loop, exhausted, or operator status_transition emits
			// no light — the reject (for a fail loop) already showed the red, and
			// an operator move is not a workflow step.
		}
	}

	// Current (in-progress) stage — shown ONLY on a non-terminal story with a LIVE
	// engagement (sty_39644f69). The pulse is the keep-alive signal re-unified with
	// the old activity spinner: without a live lease it must stay dark, even when a
	// stale unresolved review_requested row lingers in the ledger (a dangling
	// request on an abandoned story would otherwise pulse forever). liveEngagement
	// is computed by the caller from the row's engagement lease.
	if !isTerminalReviewStatus(status) && liveEngagement {
		emitted := false
		for i := len(reqOrder) - 1; i >= 0; i-- {
			pr := reqOrder[i]
			if reqByKey[pr.key] > resolvedByKey[pr.key] {
				lights = append(lights, reviewLight{Index: stageOf(pr.key), State: "current", Gate: pr.gate})
				emitted = true
				break
			}
		}
		// No open review request, but the story is actively WORKING a stage (it has
		// already cleared at least one and isn't blocked/backlog) — the gate for the
		// current stage simply hasn't been requested yet. Without this the strip goes
		// dark between gates and the in-progress signal vanishes (sty_c049bb5f, a
		// sty_6cfaa15e follow-up). Key the pulsing "current" light by the story's
		// current lifecycle status — the from-state the next forward transition will
		// carry — so the indicator is always present while work is in progress.
		if !emitted && len(lights) > 0 && isWorkingReviewStatus(status) {
			lights = append(lights, reviewLight{Index: stageOf(strings.ToLower(strings.TrimSpace(status))), State: "current"})
		}
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

// isWorkingReviewStatus reports whether a story is actively progressing a stage
// — the precondition for the between-gates fallback "current" light. A story not
// yet started (backlog), stuck (blocked), or finished (terminal) is NOT working,
// so it never gets the pulsing indicator without an explicit open review request.
func isWorkingReviewStatus(status string) bool {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case "", "backlog", "blocked", "done", "cancelled", "canceled":
		return false
	}
	return true
}

// isLiveEngagement reports whether a story's engagement is still LIVE — the
// keep-alive gate for the in-progress current light (sty_39644f69). A live lease
// (lease_until in the future) means a session currently holds the story; the lease
// stops renewing and expires once the keep-alive ticker dies. When no lease was
// recorded, fall back to the keep-alive tick recency (last_seen within the engaged
// "stale" window). Empty/unparseable inputs — and a story with no working
// engagement at all (only candidate reads, which never populate these) — read as
// not live. now is injected so the derivation stays pure/testable.
func isLiveEngagement(lastSeen, leaseUntil string, now time.Time) bool {
	if s := strings.TrimSpace(leaseUntil); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return now.Before(t)
		}
	}
	if s := strings.TrimSpace(lastSeen); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return now.Sub(t) <= engRedSecsFallback*time.Second
		}
	}
	return false
}

// gateFromReviewBody pulls the gate name out of a review_requested body, whose
// shape is "gate <name>: from <state>". Verdict rows carry their gate in the
// payload, not the body, so this is the request-row parser.
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

// fromStateFromReviewBody pulls the from-state out of a review_requested body
// ("gate <name>: from <state>") — the stage key for the in-progress "current"
// light, matching the from_status the resolving verdict/transition carries.
func fromStateFromReviewBody(body string) string {
	const marker = " from "
	if i := strings.LastIndex(body, marker); i >= 0 {
		return strings.TrimSpace(body[i+len(marker):])
	}
	return ""
}

// stPayload is the structured slice of a status_transition / review_reject
// payload the lights derivation reads (from/to states, the governing gate, and
// the v2 classification markers).
type stPayload struct {
	From      string `json:"from_status"`
	To        string `json:"to_status"`
	Gate      string `json:"gate"`
	Trigger   string `json:"trigger"`
	On        string `json:"on"`
	Exhausted bool   `json:"exhausted"`
}

func parsePayload(payload json.RawMessage) stPayload {
	var p stPayload
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &p)
	}
	p.From = strings.TrimSpace(p.From)
	p.To = strings.TrimSpace(p.To)
	p.Gate = strings.TrimSpace(p.Gate)
	return p
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
			ev := reviewEvent{Kind: e.Kind, When: e.When.UTC().Format(time.RFC3339Nano)}
			switch e.Kind {
			case "review_requested":
				ev.Gate = gateFromReviewBody(e.Kind, e.Body)
				ev.FromStatus = fromStateFromReviewBody(e.Body)
			case "review_reject":
				p := parsePayload(e.Payload)
				ev.Gate = p.Gate
				ev.FromStatus = p.From
			}
			// review_accept is intentionally left with no fields — buildReviewLights
			// ignores it (the forward status_transition records the pass).
			return ev
		},
	); err != nil {
		return nil, err
	}
	if err := collect(
		verb.LedgerListRequest{ProjectID: projectID, Kind: "status_transition"},
		func(e lightRow) reviewEvent {
			p := parsePayload(e.Payload)
			checkpoint := p.Trigger == "checkpoint" || strings.Contains(e.Body, "(checkpoint)")
			// A FORWARD gate pass: gate-driven, not a checkpoint, not a fail-loop
			// (`on:fail`) or exhausted move, and it actually advances (has a to).
			forward := !checkpoint && p.Gate != "" && p.On != "fail" && !p.Exhausted && p.To != ""
			return reviewEvent{
				Kind:       "status_transition",
				Gate:       p.Gate,
				FromStatus: p.From,
				Transition: transitionLabel(p.From, p.To, e.Body),
				Checkpoint: checkpoint,
				Forward:    forward,
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
// step) and engagement-aware: the in-progress current light is gated on a live
// engagement lease (sty_39644f69), so it MUST run after attachEngagements has
// populated each row's EngLastSeen/EngLeaseUntil. A failed read degrades to no
// lights, never a page error.
func attachReviewLights(ctx context.Context, projectID string, stories []storyRow) {
	byStory, err := latestReviewEvents(ctx, projectID)
	if err != nil {
		arbor.WarnCtx(ctx, "project_detail: review lights", "id", projectID, "err", err)
		return
	}
	now := time.Now().UTC()
	for i := range stories {
		if evts, ok := byStory[stories[i].ID]; ok {
			live := isLiveEngagement(stories[i].EngLastSeen, stories[i].EngLeaseUntil, now)
			stories[i].Reviews = buildReviewLights(evts, stories[i].Status, live)
		}
	}
}
