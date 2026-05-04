package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/bobmcallan/satellites/internal/session"
)

// headerSessionID returns the session id mcp-go's Streamable HTTP
// transport attached to the request context (extracted from the
// Mcp-Session-Id header per the 2025-03-26 spec). Empty when the call
// originated from stdio, in-process tests, or a client that didn't
// echo the header. story_31975268.
func headerSessionID(ctx context.Context) string {
	sess := mcpserver.ClientSessionFromContext(ctx)
	if sess == nil {
		return ""
	}
	return sess.SessionID()
}

// resolveSessionID picks the session id for handlers that accept the
// id either on the request body (legacy / stdio path) or via the
// Mcp-Session-Id header (Streamable HTTP). The body argument wins on
// conflict so test callers can override. story_31975268.
func resolveSessionID(ctx context.Context, bodyValue string) string {
	if bodyValue != "" {
		return bodyValue
	}
	return headerSessionID(ctx)
}

// resolveSessionStaleness returns the configured claim-staleness window.
// Env SATELLITES_SESSION_STALENESS (seconds) overrides the default.
func resolveSessionStaleness() time.Duration {
	if raw := os.Getenv("SATELLITES_SESSION_STALENESS"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return session.StalenessDefault
}

// handleSessionWhoami returns the caller's registered session row, or
// a structured not-registered error.
func (s *Server) handleSessionWhoami(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	caller, _ := UserFrom(ctx)
	sessionID := resolveSessionID(ctx, req.GetString("session_id", ""))
	if sessionID == "" {
		body, _ := json.Marshal(map[string]any{
			"error":   "session_id_required",
			"message": "session_whoami needs a session id — supply via Mcp-Session-Id header or body session_id arg",
		})
		return mcpgo.NewToolResultError(string(body)), nil
	}
	sess, err := s.sessions.Get(ctx, caller.UserID, sessionID)
	if err != nil {
		body, _ := json.Marshal(map[string]any{"error": "session_not_registered"})
		return mcpgo.NewToolResultError(string(body)), nil
	}
	payload := map[string]any{
		"user_id":       sess.UserID,
		"session_id":    sess.SessionID,
		"source":        sess.Source,
		"registered_at": sess.Registered,
		"last_seen_at":  sess.LastSeenAt,
	}
	if sess.WorkspaceID != "" {
		payload["workspace_id"] = sess.WorkspaceID
	}
	body, _ := json.Marshal(payload)
	return mcpgo.NewToolResultText(string(body)), nil
}

// handleSessionRegister lets the SessionStart hook and API-key flows
// populate the registry. In production this is driven by the harness;
// exposing it as a verb keeps tests honest and gives callers a way to
// re-register after an unexpected restart.
func (s *Server) handleSessionRegister(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	caller, _ := UserFrom(ctx)
	// story_31975268: server-mint session_id when neither the body arg
	// nor the Mcp-Session-Id header carries one. Streamable HTTP clients
	// receive the minted id via the initialize response header and echo
	// it on subsequent calls; stdio/test callers may pass session_id
	// explicitly as a body arg.
	sessionID := resolveSessionID(ctx, req.GetString("session_id", ""))
	source := req.GetString("source", session.SourceSessionStart)
	workspaceID := req.GetString("workspace_id", "")
	projectID := req.GetString("project_id", "")
	now := s.nowUTC()

	// story_cef068fe: session_resume semantics — when project_id is
	// supplied AND no explicit session_id was carried, look up the most
	// recent active (non-stale) session for (caller.user, project_id)
	// and reuse it. Allows a CLI restart to recover the prior session
	// row without orphaning in-flight work claimed by it. Stale
	// sessions are skipped; a fresh one is minted instead.
	resumed := false
	if sessionID == "" && projectID != "" {
		if prior, ok := s.findFreshSessionForProject(ctx, caller.UserID, projectID, now); ok {
			sessionID = prior.SessionID
			resumed = true
		}
	}
	if sessionID == "" {
		sessionID = uuid.NewString()
	}
	sess, err := s.sessions.Register(ctx, caller.UserID, sessionID, source, now)
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	if workspaceID != "" {
		if updated, err := s.sessions.SetWorkspace(ctx, caller.UserID, sessionID, workspaceID, now); err == nil {
			sess = updated
		}
	}
	if projectID != "" {
		if updated, err := s.sessions.SetActiveProject(ctx, caller.UserID, sessionID, projectID, now); err == nil {
			sess = updated
		}
	}
	payload := map[string]any{
		"user_id":       sess.UserID,
		"session_id":    sess.SessionID,
		"source":        sess.Source,
		"registered_at": sess.Registered,
		"last_seen_at":  sess.LastSeenAt,
		"resumed":       resumed,
	}
	if sess.WorkspaceID != "" {
		payload["workspace_id"] = sess.WorkspaceID
	}
	if sess.ActiveProjectID != "" {
		payload["active_project_id"] = sess.ActiveProjectID
	}
	body, _ := json.Marshal(payload)
	return mcpgo.NewToolResultText(string(body)), nil
}

// findFreshSessionForProject returns the caller's most recent
// non-stale session bound to the given project (resume scope per
// story_cef068fe). The session must:
//   - belong to userID,
//   - have ActiveProjectID == projectID,
//   - have LastSeenAt within SATELLITES_SESSION_STALENESS of now.
//
// Returns ok=false when nothing matches; the caller mints a fresh id.
func (s *Server) findFreshSessionForProject(ctx context.Context, userID, projectID string, now time.Time) (session.Session, bool) {
	if s.sessions == nil {
		return session.Session{}, false
	}
	rows, err := s.sessions.ListAll(ctx)
	if err != nil {
		return session.Session{}, false
	}
	staleness := resolveSessionStaleness()
	var best session.Session
	found := false
	for _, row := range rows {
		if row.UserID != userID || row.ActiveProjectID != projectID {
			continue
		}
		if session.IsStale(row, now, staleness) {
			continue
		}
		if !found || row.LastSeenAt.After(best.LastSeenAt) {
			best = row
			found = true
		}
	}
	return best, found
}
