// GET /api/engagements?project_id=… — the server-side processing view
// (sty_84c55d0d): the latest engagement signal per (session, story) for a
// project, as JSON. The client's engagement ticker lands `engagement:*`
// ledger rows (phase in body, seq/lease_until in payload); this endpoint
// folds them to the newest row per pair so the portal's processing indicator
// (the engagement-age icon) has one stable data source. Reads ride the
// ledger_list verb — transport layering forbids importing internal/ledger.

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/verb"
)

// engagementWindow bounds how far back the fold looks: a pair silent longer
// than this is gone from the view (the portal treats absence as idle).
const engagementWindow = 24 * time.Hour

// engagementView is one (session, story) pair's latest signal.
type engagementView struct {
	SessionID  string    `json:"session_id"`
	StoryID    string    `json:"story_id"`
	Phase      string    `json:"phase"`
	LeaseUntil string    `json:"lease_until,omitempty"`
	LastSeen   time.Time `json:"last_seen"`
}

func engagementsHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := cfg.Sessions.UserID(r)
		if err != nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
		if projectID == "" {
			http.Error(w, `{"error":"project_id required"}`, http.StatusBadRequest)
			return
		}
		ctx := withSessionUser(r.Context(), cfg, userID)
		views, err := latestEngagements(ctx, projectID, time.Now().UTC())
		if err != nil {
			http.Error(w, `{"error":"engagements unavailable"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(views)
	}
}

// latestEngagements pages the project's engagement:* rows for the window and
// folds them to the newest per (session, story).
func latestEngagements(ctx context.Context, projectID string, now time.Time) ([]engagementView, error) {
	byPair := map[string]engagementView{}
	cursor := ""
	for {
		req, err := json.Marshal(verb.LedgerListRequest{
			ProjectID:    projectID,
			KindPrefix:   "engagement:",
			CreatedAfter: now.Add(-engagementWindow).Format(time.RFC3339),
			Limit:        2000,
			Cursor:       cursor,
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
			foldEngagement(byPair, e.SessionID, e.StoryID, e.Body, e.Payload, e.CreatedAt)
		}
		if resp.NextCursor == "" {
			break
		}
		cursor = resp.NextCursor
	}
	out := make([]engagementView, 0, len(byPair))
	for _, v := range byPair {
		out = append(out, v)
	}
	return out, nil
}

// foldEngagement records one engagement row into the per-(session, story)
// view — entries arrive oldest-first, so the last write per pair wins. Pure
// for tests. Rows missing either correlation id carry no pair and are
// dropped; phase prefers the payload field, falling back to the body.
func foldEngagement(byPair map[string]engagementView, sessionID, storyID, body string, payload json.RawMessage, createdAt time.Time) {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(storyID) == "" {
		return
	}
	var p struct {
		Phase      string `json:"phase"`
		LeaseUntil string `json:"lease_until"`
	}
	_ = json.Unmarshal(payload, &p)
	phase := p.Phase
	if phase == "" {
		phase = body
	}
	byPair[sessionID+"|"+storyID] = engagementView{
		SessionID: sessionID, StoryID: storyID,
		Phase: phase, LeaseUntil: p.LeaseUntil, LastSeen: createdAt,
	}
}
