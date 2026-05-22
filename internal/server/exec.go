package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/bobmcallan/satellites/internal/verb"
)

// execHandler is the CLI ↔ server transport: a thin HTTP wrapper
// around verb.Dispatch. POST /api/v1/exec/<verb_name> with the verb's
// JSON request body returns the verb's JSON response. Auth is the
// standard /mcp gate (api-key or JWT) wired by Store.Middleware.
//
// Why a parallel route to /mcp: MCP-SDK clients (Claude) speak
// JSON-RPC over the streamable HTTP transport. The satellites CLI is
// a plain HTTP client — a flat POST is the smallest surface that
// matches its shape. Both routes ultimately call verb.Dispatch.
func execHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/api/v1/exec/")
		if name == "" || strings.Contains(name, "/") {
			http.Error(w, "exec: bad path; expected /api/v1/exec/<verb>", http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "exec: read body", http.StatusBadRequest)
			return
		}
		resp, err := verb.Dispatch(r.Context(), name, json.RawMessage(body))
		if err != nil {
			// Verb-layer errors are returned as JSON so the CLI can
			// surface a structured message rather than text. Sentinel
			// errors from internal/verb map to canonical HTTP statuses;
			// everything else falls back to 400.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(verbErrorStatus(err))
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}
}

// verbErrorStatus maps a verb-layer error to its HTTP status. The
// classification mirrors what the CLI and MCP consumers expect: a 403
// signals a forbidden authorisation context (you're authenticated but
// not entitled), 404 signals a missing resource, 401 signals missing
// credentials. Anything unclassified is a 400 — verbs validate their
// own input.
func verbErrorStatus(err error) int {
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
