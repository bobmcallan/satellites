package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/verb"
)

// storyStatusHandler is the portal-only quick-status flip behind the
// expanded-row status buttons in /projects/{id}. Sessions-gated; wraps
// document_upsert with the patch-by-id mode. POST shape mirrors v4's
// /api/stories/{id}/status — { "status": "<value>" } — so the JS
// factory ported from v4 needs no transport reshape.
func storyStatusHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		userID, err := cfg.Sessions.UserID(r)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// Path: /api/stories/{id}/status
		rest := strings.TrimPrefix(r.URL.Path, "/api/stories/")
		id, suffix, ok := strings.Cut(rest, "/")
		if !ok || suffix != "status" || id == "" {
			http.Error(w, "bad path; expected /api/stories/<id>/status", http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		var payload struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(body, &payload); err != nil || payload.Status == "" {
			http.Error(w, "body must be {\"status\":\"...\"}", http.StatusBadRequest)
			return
		}
		ctx := withSessionUser(r.Context(), cfg, userID)
		req := verb.DocumentUpsertRequest{ID: id, Status: &payload.Status}
		reqBody, _ := json.Marshal(req)
		resp, err := verb.Dispatch(ctx, "document_upsert", reqBody)
		if err != nil {
			arbor.WarnCtx(ctx, "story_status: dispatch", "id", id, "err", err)
			http.Error(w, err.Error(), storyStatusErrorCode(err))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}
}

func storyStatusErrorCode(err error) int {
	switch {
	case errors.Is(err, verb.ErrUnauthorized):
		return http.StatusUnauthorized
	case errors.Is(err, verb.ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, verb.ErrNotFound):
		return http.StatusNotFound
	default:
		return http.StatusBadRequest
	}
}
