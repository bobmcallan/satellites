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

// reviewLight is one dot in a story row's review-history strip (sty_14f07f22):
// a single reviewer-gate outcome. State is "pass" (a review_accept), "fail" (a
// review_reject), or "pending" (a review_requested with no verdict yet). Gate is
// the gate name for the hover title; When is its RFC3339 timestamp.
type reviewLight struct {
	State string
	Gate  string
	When  string
}

// reviewEvent is the transport-neutral projection of a review_* ledger row that
// buildReviewLights folds into the light strip — kept separate so the derivation
// is unit-testable without the ledger.
type reviewEvent struct {
	Kind string // review_requested | review_accept | review_reject
	Gate string // gate name (parsed from a review_requested body), else ""
	When string // RFC3339
}

// buildReviewLights folds a story's chronological review_* events into the light
// strip: one light per VERDICT — review_accept → pass, review_reject → fail — in
// order, so a gate that ran repeatedly contributes one light per run. A trailing
// review_requested with no following verdict yields a single "pending" light.
// Each verdict carries the gate name from its preceding review_requested (the
// verdict row itself does not name the gate).
func buildReviewLights(evts []reviewEvent) []reviewLight {
	lights := make([]reviewLight, 0, len(evts))
	pendingGate := ""
	pending := false
	for _, e := range evts {
		switch e.Kind {
		case "review_requested":
			pendingGate = e.Gate
			pending = true
		case "review_accept":
			lights = append(lights, reviewLight{State: "pass", Gate: firstNonEmpty(e.Gate, pendingGate), When: e.When})
			pending, pendingGate = false, ""
		case "review_reject":
			lights = append(lights, reviewLight{State: "fail", Gate: firstNonEmpty(e.Gate, pendingGate), When: e.When})
			pending, pendingGate = false, ""
		}
	}
	if pending {
		lights = append(lights, reviewLight{State: "pending", Gate: pendingGate})
	}
	return lights
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

// latestReviewLights fetches the project's review_* ledger rows in one batched,
// paginated ledger_list (kind_prefix "review_") and derives the ordered light
// strip per story_id — one ledger query for the whole list render, bucketed by
// story (mirrors latestEngagements). Read-only.
func latestReviewLights(ctx context.Context, projectID string) (map[string][]reviewLight, error) {
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
	out := make(map[string][]reviewLight, len(byStory))
	for sid, evts := range byStory {
		// ledger_list returns oldest-first; sort defensively so the strip is
		// chronological even across pages.
		sort.SliceStable(evts, func(i, j int) bool { return evts[i].When < evts[j].When })
		out[sid] = buildReviewLights(evts)
	}
	return out, nil
}

// attachReviewLights decorates the story rows with their review-history light
// strip (sty_14f07f22), via one batched ledger read for the project. A failed
// read degrades to no lights, never a page error.
func attachReviewLights(ctx context.Context, projectID string, stories []storyRow) {
	byStory, err := latestReviewLights(ctx, projectID)
	if err != nil {
		arbor.WarnCtx(ctx, "project_detail: review lights", "id", projectID, "err", err)
		return
	}
	for i := range stories {
		if lights, ok := byStory[stories[i].ID]; ok {
			stories[i].Reviews = lights
		}
	}
}
