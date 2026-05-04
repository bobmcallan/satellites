package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
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

// handleContractClaim is the keystone claim verb: it runs the
// process-order gate, verifies the session, writes action-claim and
// optional plan ledger rows, and transitions the CI to claimed.
// Same-session re-claim is an amend: prior plan + action_claim rows
// are dereferenced and rewritten.
func (s *Server) handleContractClaim(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	start := time.Now()
	caller, _ := UserFrom(ctx)
	ciID, err := req.RequireString("contract_instance_id")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	// session_id is sourced from the Mcp-Session-Id header by default
	// (story_31975268); body arg accepted as override for stdio/tests.
	sessionID := resolveSessionID(ctx, req.GetString("session_id", ""))
	if sessionID == "" {
		body, _ := json.Marshal(map[string]any{
			"error":   "session_id_required",
			"message": "contract_claim needs a session id — supply via Mcp-Session-Id header or body session_id arg",
		})
		return mcpgo.NewToolResultError(string(body)), nil
	}
	skillsUsed := req.GetStringSlice("skills_used", nil)
	planMarkdown := req.GetString("plan_markdown", "")
	if legacy := req.GetStringSlice("permissions_claim", nil); len(legacy) > 0 {
		body, _ := json.Marshal(map[string]any{
			"error":   "permissions_claim_retired",
			"message": "permissions_claim is no longer accepted; allocate a type=agent doc and pass agent_id (story_cc55e093)",
		})
		return mcpgo.NewToolResultError(string(body)), nil
	}

	memberships := s.resolveCallerMemberships(ctx, caller)
	ci, err := s.contracts.GetByID(ctx, ciID, memberships)
	agentID := req.GetString("agent_id", "")
	if agentID == "" && err == nil && ci.AgentID != "" {
		agentID = ci.AgentID
	}
	if agentID == "" {
		body, _ := json.Marshal(map[string]any{
			"error":   "agent_required",
			"message": "contract_claim could not resolve an agent — supply agent_id arg or ensure the CI has a stamped AgentID (orchestrator_compose_plan)",
		})
		return mcpgo.NewToolResultError(string(body)), nil
	}
	if err != nil {
		body, _ := json.Marshal(map[string]any{"error": "ci_not_found", "contract_instance_id": ciID})
		return mcpgo.NewToolResultError(string(body)), nil
	}

	// Resolve permission_patterns from the allocated agent doc. The
	// docs store is required for the strict path — return a structured
	// error rather than silently degrading.
	if s.docs == nil {
		body, _ := json.Marshal(map[string]any{
			"error":   "doc_store_unavailable",
			"message": "contract_claim requires the document store to resolve agent_id",
		})
		return mcpgo.NewToolResultError(string(body)), nil
	}
	// Project-pivot the agent doc lookup (sty_94d8a501,
	// epic:status-bus-v1). Pre-fix, the lookup ran with workspace
	// memberships and rejected every system-scope agent (developer_agent,
	// releaser_agent, story_close_agent, agent_claude_orchestrator,
	// agent_gemini_reviewer) for any caller who isn't a member of the
	// system workspace — even though those docs are global by design.
	// Post-fix: lookup is unfiltered, then accepted iff the agent's
	// scope authorises this CI:
	//   - scope=system → always (system docs are the substrate's
	//     identity, not anyone's tenant data).
	//   - scope=project AND agent.project_id == ci.project_id → same-project.
	//   - otherwise → reject `agent_not_authorized`.
	agentDoc, derr := s.docs.GetByID(ctx, agentID, nil)
	if derr != nil {
		body, _ := json.Marshal(map[string]any{
			"error":    "agent_not_found",
			"agent_id": agentID,
		})
		return mcpgo.NewToolResultError(string(body)), nil
	}
	if agentDoc.Type != document.TypeAgent {
		body, _ := json.Marshal(map[string]any{
			"error":    "agent_id_wrong_type",
			"agent_id": agentID,
			"type":     agentDoc.Type,
		})
		return mcpgo.NewToolResultError(string(body)), nil
	}
	if !agentScopeAuthorises(agentDoc, ci.ProjectID) {
		agentProj := ""
		if agentDoc.ProjectID != nil {
			agentProj = *agentDoc.ProjectID
		}
		body, _ := json.Marshal(map[string]any{
			"error":       "agent_not_authorized",
			"agent_id":    agentID,
			"agent_scope": agentDoc.Scope,
			"agent_proj":  agentProj,
			"ci_project":  ci.ProjectID,
			"reason":      "agent must be scope=system OR scope=project with matching project_id",
		})
		return mcpgo.NewToolResultError(string(body)), nil
	}
	settings, serr := document.UnmarshalAgentSettings(agentDoc.Structured)
	if serr != nil {
		return mcpgo.NewToolResultError(serr.Error()), nil
	}
	permissionsClaim := settings.PermissionPatterns

	// Session registry: must be registered + not stale. Gate runs
	// before grant resolution so a stale/missing session short-circuits
	// before we touch the grant store.
	if err := s.verifyCallerSession(ctx, caller.UserID, sessionID, s.nowUTC()); err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}

	// Same-agent amend path: CI already claimed by the same agent_id →
	// dereference prior action_claim + plan rows and write fresh ones.
	amend := ci.Status == contract.StatusClaimed && ci.AgentID == agentID
	if ci.Status == contract.StatusClaimed && !amend {
		body, _ := json.Marshal(map[string]any{
			"error":                "agent_mismatch",
			"contract_instance_id": ciID,
			"claimed_agent_id":     ci.AgentID,
		})
		return mcpgo.NewToolResultError(string(body)), nil
	}

	if !amend {
		if err := contract.CheckCIReady(ci); err != nil {
			return mcpgo.NewToolResultError(marshalGateRejection(err)), nil
		}
		peers, err := s.contracts.List(ctx, ci.StoryID, memberships)
		if err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
		if err := contract.PredecessorGate(peers, ci); err != nil {
			return mcpgo.NewToolResultError(marshalGateRejection(err)), nil
		}
	}

	// If amending, dereference prior action_claim + plan rows before
	// writing fresh ones.
	if amend {
		if ci.PlanLedgerID != "" {
			_, _ = s.ledger.Dereference(ctx, ci.PlanLedgerID, "amended", caller.UserID, s.nowUTC(), memberships)
		}
		// The action_claim row shares the CI scope but isn't tracked on
		// the CI directly — find the latest active action_claim row for
		// this CI and dereference it.
		if priorAC := s.findLatestActionClaim(ctx, ci, memberships); priorAC != "" {
			_, _ = s.ledger.Dereference(ctx, priorAC, "amended", caller.UserID, s.nowUTC(), memberships)
		}
	}

	now := s.nowUTC()
	acStructured, _ := json.Marshal(map[string]any{
		"permissions_claim": permissionsClaim,
		"skills_used":       skillsUsed,
		"agent_id":          agentID,
		"source":            "agent_document",
	})
	acRow, err := s.ledger.Append(ctx, ledger.LedgerEntry{
		WorkspaceID: ci.WorkspaceID,
		ProjectID:   ci.ProjectID,
		StoryID:     ledger.StringPtr(ci.StoryID),
		ContractID:  ledger.StringPtr(ci.ID),
		Type:        ledger.TypeActionClaim,
		Tags:        []string{"kind:action-claim", "phase:" + ci.ContractName},
		Content:     "action claim",
		Structured:  acStructured,
		CreatedBy:   caller.UserID,
	}, now)
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}

	var planRowID string
	if planMarkdown != "" {
		planRow, err := s.ledger.Append(ctx, ledger.LedgerEntry{
			WorkspaceID: ci.WorkspaceID,
			ProjectID:   ci.ProjectID,
			StoryID:     ledger.StringPtr(ci.StoryID),
			ContractID:  ledger.StringPtr(ci.ID),
			Type:        ledger.TypePlan,
			Tags:        []string{"kind:plan", "phase:" + ci.ContractName},
			Content:     planMarkdown,
			CreatedBy:   caller.UserID,
		}, now)
		if err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
		planRowID = planRow.ID
	}

	if !amend {
		if _, err := s.contracts.Claim(ctx, ci.ID, "", now, memberships); err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
	}
	if agentID != "" {
		if _, err := s.contracts.SetAgent(ctx, ci.ID, agentID, now, memberships); err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
	}
	if planRowID != "" {
		planRef := planRowID
		if _, err := s.contracts.UpdateLedgerRefs(ctx, ci.ID, &planRef, nil, caller.UserID, now, memberships); err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
	}

	body, _ := json.Marshal(map[string]any{
		"contract_instance_id":   ci.ID,
		"story_id":               ci.StoryID,
		"status":                 contract.StatusClaimed,
		"amended":                amend,
		"action_claim_ledger_id": acRow.ID,
		"plan_ledger_id":         planRowID,
	})
	s.logger.Info().
		Str("method", "tools/call").
		Str("tool", "contract_claim").
		Str("ci_id", ci.ID).
		Bool("amended", amend).
		Int64("duration_ms", time.Since(start).Milliseconds()).
		Msg("mcp tool call")
	return mcpgo.NewToolResultText(string(body)), nil
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
	// row without orphaning in-flight CIs claimed by it. Stale sessions
	// are skipped; a fresh one is minted instead.
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

// verifyCallerSession returns a structured error body string on
// failure. On success it touches the registry and returns nil.
func (s *Server) verifyCallerSession(ctx context.Context, userID, sessionID string, now time.Time) error {
	sess, err := s.sessions.Get(ctx, userID, sessionID)
	if err != nil {
		body, _ := json.Marshal(map[string]any{"error": "session_not_registered"})
		return errors.New(string(body))
	}
	if session.IsStale(sess, now, resolveSessionStaleness()) {
		body, _ := json.Marshal(map[string]any{"error": "session_stale", "last_seen_at": sess.LastSeenAt.Format(time.RFC3339)})
		return errors.New(string(body))
	}
	if _, err := s.sessions.Touch(ctx, userID, sessionID, now); err != nil {
		return fmt.Errorf("session: touch: %w", err)
	}
	return nil
}

// findLatestActionClaim searches the ledger for the latest active
// kind:action-claim row scoped to the CI. Returns empty string when
// none exists.
func (s *Server) findLatestActionClaim(ctx context.Context, ci contract.ContractInstance, memberships []string) string {
	rows, err := s.ledger.List(ctx, ci.ProjectID, ledger.ListOptions{
		Type: ledger.TypeActionClaim,
		Tags: []string{"kind:action-claim"},
	}, memberships)
	if err != nil {
		return ""
	}
	for _, r := range rows {
		if r.ContractID != nil && *r.ContractID == ci.ID && r.Status == ledger.StatusActive {
			return r.ID
		}
	}
	return ""
}

// agentScopeAuthorises reports whether agentDoc is allowed to be
// allocated to a CI in ciProjectID under the project-pivoted tenancy
// rule (sty_94d8a501).
//
// Two valid shapes:
//
//  1. agentDoc.Scope == document.ScopeSystem — system agents are global
//     by definition (`developer_agent`, `releaser_agent`, etc.). Any
//     project may use them.
//  2. agentDoc.Scope == document.ScopeProject AND
//     *agentDoc.ProjectID == ciProjectID — project-scope agents are
//     bound to one project; cross-project use is the bug we're guarding
//     against.
//
// Anything else (workspace-scope, missing project_id on a project-scope
// row, mismatched project) is rejected.
func agentScopeAuthorises(agentDoc document.Document, ciProjectID string) bool {
	switch agentDoc.Scope {
	case document.ScopeSystem:
		return true
	case document.ScopeProject:
		return agentDoc.ProjectID != nil && *agentDoc.ProjectID == ciProjectID
	default:
		return false
	}
}

// marshalGateRejection is the shared renderer for *contract.GateRejection.
func marshalGateRejection(err error) string {
	var gr *contract.GateRejection
	if errors.As(err, &gr) {
		b, _ := json.Marshal(map[string]any{
			"error":    gr.Kind,
			"ci_id":    gr.CIID,
			"blocking": gr.Blocking,
			"current":  gr.Current,
			"message":  gr.Error(),
		})
		return string(b)
	}
	b, _ := json.Marshal(map[string]any{"error": "claim_rejected", "message": err.Error()})
	return string(b)
}
