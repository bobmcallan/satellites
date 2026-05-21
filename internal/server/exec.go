package server

import (
	"encoding/json"
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
			// surface a structured message rather than text.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}
}
