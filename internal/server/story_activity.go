// GET /api/stories/{id}/activity — live "being worked now" state for a story
// (epic:dynamic-workflow-status, order:1, sty_8e21c3ec).
//
// Read-only and session-gated. It derives activity from the engagement events
// the client now flushes to the ledger in real time (kind engagement:*): a story
// is active when its latest engagement event is a non-close kind within
// activityWindow. The portal's activity indicator (order:2) refetches this on the
// activity:<id> SSE topic and lets it decay once the window lapses. Distinct from
// the 2h edit lease — this is the short "is the agent here right now" window.

package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/verb"
)

// activityWindow is how recently an engagement event must have landed for a
// story to read as "being worked now". Kept short so the indicator decays
// quickly when work stops; the START door touches at a shorter cadence
// (activityTouchWindow, client-side) so an actively-edited story stays inside it.
const activityWindow = 3 * time.Minute

// engagementKindPrefix tags the ledger rows the engagement flush appends
// (kind "engagement:<engage|phase|touch|close|...>").
const engagementKindPrefix = "engagement:"

// engagementCloseKind retires an engagement — the latest event being a close
// means the story is no longer being worked, regardless of recency.
const engagementCloseKind = "engagement:close"

// storyActivity is the activity endpoint's JSON response.
type storyActivity struct {
	Active  bool   `json:"active"`
	Since   string `json:"since,omitempty"`   // RFC3339 of the latest engagement event
	Session string `json:"session,omitempty"` // session driving the activity
}

// activityEntry is the slice of a ledger_list row this handler needs. Decoded
// locally rather than via verb.LedgerListResponse so the server layer does not
// import internal/ledger (the substrate-domain layering boundary).
type activityEntry struct {
	Kind      string    `json:"kind"`
	SessionID string    `json:"session_id"`
	CreatedAt time.Time `json:"created_at"`
}

func storyActivityHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := cfg.Sessions.UserID(r)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// Path: /api/stories/{id}/activity
		rest := strings.TrimPrefix(r.URL.Path, "/api/stories/")
		id, suffix, ok := strings.Cut(rest, "/")
		if !ok || suffix != "activity" || id == "" {
			http.Error(w, "bad path; expected /api/stories/<id>/activity", http.StatusBadRequest)
			return
		}
		ctx := withSessionUser(r.Context(), cfg, userID)
		body, _ := json.Marshal(verb.LedgerListRequest{StoryID: id, Limit: 200})
		raw, err := verb.Dispatch(ctx, "ledger_list", body)
		if err != nil {
			arbor.WarnCtx(ctx, "story_activity: ledger_list", "id", id, "err", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var resp struct {
			Entries []activityEntry `json:"entries"`
		}
		_ = json.Unmarshal(raw, &resp)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(computeActivity(resp.Entries, time.Now().UTC()))
	}
}

// computeActivity reports a story active when its single most-recent engagement
// event is a non-close kind within activityWindow of now. Pure for testability;
// order-independent (ledger_list returns oldest-first), so it tracks the max
// CreatedAt rather than assuming a sort.
func computeActivity(entries []activityEntry, now time.Time) storyActivity {
	var latest *activityEntry
	for i := range entries {
		if !strings.HasPrefix(entries[i].Kind, engagementKindPrefix) {
			continue
		}
		if latest == nil || entries[i].CreatedAt.After(latest.CreatedAt) {
			latest = &entries[i]
		}
	}
	if latest == nil || latest.Kind == engagementCloseKind || now.Sub(latest.CreatedAt) > activityWindow {
		return storyActivity{Active: false}
	}
	return storyActivity{
		Active:  true,
		Since:   latest.CreatedAt.UTC().Format(time.RFC3339),
		Session: latest.SessionID,
	}
}
