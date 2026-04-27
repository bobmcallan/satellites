package permhook

import (
	"encoding/json"
	"net/http"

	"github.com/ternarybob/arbor"
)

// Handler is the HTTP shim over Resolver. Mounts at POST /hooks/enforce
// per AC1. Story_c08856b2.
type Handler struct {
	Resolver *Resolver
	Logger   arbor.ILogger
}

// hookRequest mirrors the PreToolUse payload shape the Claude Code
// harness POSTs. Extra fields are ignored.
type hookRequest struct {
	Tool      string `json:"tool"`
	SessionID string `json:"session_id"`
}

// Register attaches the /hooks/enforce route. Satisfies
// httpserver.RouteRegistrar.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /hooks/enforce", h.serveEnforce)
}

func (h *Handler) serveEnforce(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var req hookRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, Result{
			Decision: DecisionDeny,
			Reason:   "bad_request",
			Source:   SourceNone,
		})
		return
	}
	res := h.Resolver.Resolve(r.Context(), req.SessionID, req.Tool)
	if h.Logger != nil {
		h.Logger.Debug().
			Str("session_id", req.SessionID).
			Str("tool", req.Tool).
			Str("decision", res.Decision).
			Str("reason", res.Reason).
			Str("source", res.Source).
			Msg("permhook enforce decision")
	}
	writeJSON(w, http.StatusOK, res)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	body, _ := json.Marshal(v)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
