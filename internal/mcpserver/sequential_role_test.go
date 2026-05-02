package mcpserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/rolegrant"
	"github.com/bobmcallan/satellites/internal/session"
)

// sequentialRoleFixture wires the subset of Server agent_role_claim +
// agent_role_release need: docs, grants, sessions, ledger. Seeds the
// orchestrator role + agent so the handler resolves both.
func sequentialRoleFixture(t *testing.T) (*Server, string, string) {
	t.Helper()
	s := orchestratorTestServer(t, true)
	roleDoc, err := s.docs.GetByName(context.Background(), "", SeedRoleOrchestratorName, nil)
	require.NoError(t, err)
	agentDoc, err := s.docs.GetByName(context.Background(), "", SeedAgentOrchestratorName, nil)
	require.NoError(t, err)
	return s, roleDoc.ID, agentDoc.ID
}

func callerCtx(userID string) context.Context {
	return context.WithValue(context.Background(), userKey, CallerIdentity{UserID: userID, Email: userID + "@example.com", Source: "apikey"})
}

func claimRole(t *testing.T, s *Server, ctx context.Context, sessionID, roleID, agentID string) (*mcpgo.CallToolResult, map[string]any) {
	t.Helper()
	req := mcpgo.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"workspace_id": "wksp_sys",
		"role_id":      roleID,
		"agent_id":     agentID,
		"grantee_kind": "session",
		"grantee_id":   sessionID,
	}
	res, err := s.handleAgentRoleClaim(ctx, req)
	require.NoError(t, err)
	if res.IsError {
		return res, nil
	}
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(res.Content[0].(mcpgo.TextContent).Text), &out))
	return res, out
}

// TestSequentialRole_ClaimWithNoPriorGrant_Success — fresh session,
// first claim succeeds and stamps the grant on the session row.
func TestSequentialRole_ClaimWithNoPriorGrant_Success(t *testing.T) {
	t.Parallel()
	s, roleID, agentID := sequentialRoleFixture(t)
	ctx := callerCtx("u1")
	_, err := s.sessions.Register(ctx, "u1", "sess_a", session.SourceSessionStart, time.Now().UTC())
	require.NoError(t, err)

	res, out := claimRole(t, s, ctx, "sess_a", roleID, agentID)
	require.False(t, res.IsError)
	require.NotEmpty(t, out["grant_id"])
	if reused, ok := out["reused"].(bool); ok && reused {
		t.Fatalf("first claim must NOT be a reuse; got %+v", out)
	}

	sess, err := s.sessions.Get(ctx, "u1", "sess_a")
	require.NoError(t, err)
	assert.Equal(t, out["grant_id"], sess.OrchestratorGrantID)
}

// TestSequentialRole_ClaimSameRoleReuses — claiming the same role twice
// returns the existing grant (the §5 same-role optimisation).
func TestSequentialRole_ClaimSameRoleReuses(t *testing.T) {
	t.Parallel()
	s, roleID, agentID := sequentialRoleFixture(t)
	ctx := callerCtx("u1")
	_, err := s.sessions.Register(ctx, "u1", "sess_a", session.SourceSessionStart, time.Now().UTC())
	require.NoError(t, err)

	_, first := claimRole(t, s, ctx, "sess_a", roleID, agentID)
	_, second := claimRole(t, s, ctx, "sess_a", roleID, agentID)

	assert.Equal(t, first["grant_id"], second["grant_id"], "same-role re-claim must reuse the existing grant")
	assert.Equal(t, true, second["reused"])
}

// TestSequentialRole_ClaimDifferentRoleRejected — when the session
// already holds an active grant for role A, claiming role B is rejected
// with role_already_held carrying the live grant id and role.
func TestSequentialRole_ClaimDifferentRoleRejected(t *testing.T) {
	t.Parallel()
	s, roleID, agentID := sequentialRoleFixture(t)
	ctx := callerCtx("u1")
	_, err := s.sessions.Register(ctx, "u1", "sess_a", session.SourceSessionStart, time.Now().UTC())
	require.NoError(t, err)

	_, first := claimRole(t, s, ctx, "sess_a", roleID, agentID)
	require.NotEmpty(t, first["grant_id"])

	// Seed a *different* role + extend the agent's permitted_roles to
	// include it, so role-membership-checks are not the failure cause.
	now := time.Now().UTC()
	otherRole, err := s.docs.Create(ctx, document.Document{
		WorkspaceID: "wksp_sys",
		Type:        document.TypeRole,
		Name:        "role_other",
		Scope:       document.ScopeSystem,
		Status:      document.StatusActive,
		Structured:  []byte(`{"allowed_mcp_verbs":["document_get"]}`),
	}, now)
	require.NoError(t, err)
	patched := []byte(`{"permitted_roles":["` + roleID + `","` + otherRole.ID + `"],"tool_ceiling":["*"]}`)
	_, err = s.docs.Update(ctx, agentID, document.UpdateFields{Structured: &patched}, "test", now, nil)
	require.NoError(t, err)

	res, _ := claimRole(t, s, ctx, "sess_a", otherRole.ID, agentID)
	require.True(t, res.IsError, "claim of a different role must be rejected while a grant is held")
	errText := res.Content[0].(mcpgo.TextContent).Text
	assert.Contains(t, errText, "role_already_held")
	assert.Contains(t, errText, first["grant_id"].(string), "rejection must surface the live grant id")
}

// TestSequentialRole_ReleaseThenClaim — releasing the held grant then
// claiming a different role succeeds and stamps the new grant.
func TestSequentialRole_ReleaseThenClaim(t *testing.T) {
	t.Parallel()
	s, roleID, agentID := sequentialRoleFixture(t)
	ctx := callerCtx("u1")
	_, err := s.sessions.Register(ctx, "u1", "sess_a", session.SourceSessionStart, time.Now().UTC())
	require.NoError(t, err)

	_, first := claimRole(t, s, ctx, "sess_a", roleID, agentID)
	grantA, _ := first["grant_id"].(string)
	require.NotEmpty(t, grantA)

	// Release.
	relReq := mcpgo.CallToolRequest{}
	relReq.Params.Arguments = map[string]any{"grant_id": grantA, "reason": "phase end"}
	rel, err := s.handleAgentRoleRelease(ctx, relReq)
	require.NoError(t, err)
	require.False(t, rel.IsError)

	// Session row's OrchestratorGrantID is cleared.
	sess, err := s.sessions.Get(ctx, "u1", "sess_a")
	require.NoError(t, err)
	assert.Empty(t, sess.OrchestratorGrantID, "agent_role_release must clear the session's stamped grant id")

	// New claim succeeds — same role to keep the agent payload valid.
	res, second := claimRole(t, s, ctx, "sess_a", roleID, agentID)
	require.False(t, res.IsError)
	grantB, _ := second["grant_id"].(string)
	require.NotEmpty(t, grantB)
	assert.NotEqual(t, grantA, grantB, "fresh claim after release must mint a distinct grant id")
}

// TestSequentialRole_ReleaseWhenEmpty_NoOp — releasing a grant the
// session does not hold (e.g. already released elsewhere) is a no-op
// from the session's perspective: OrchestratorGrantID stays whatever
// it was.
func TestSequentialRole_ReleaseWhenEmpty_NoOp(t *testing.T) {
	t.Parallel()
	s, roleID, agentID := sequentialRoleFixture(t)
	ctx := callerCtx("u1")
	_, err := s.sessions.Register(ctx, "u1", "sess_a", session.SourceSessionStart, time.Now().UTC())
	require.NoError(t, err)

	_, first := claimRole(t, s, ctx, "sess_a", roleID, agentID)
	grantA, _ := first["grant_id"].(string)
	require.NotEmpty(t, grantA)

	// Release the same grant twice; second is redundant.
	relReq := mcpgo.CallToolRequest{}
	relReq.Params.Arguments = map[string]any{"grant_id": grantA, "reason": "first"}
	_, _ = s.handleAgentRoleRelease(ctx, relReq)

	relReq2 := mcpgo.CallToolRequest{}
	relReq2.Params.Arguments = map[string]any{"grant_id": grantA, "reason": "second"}
	res, err := s.handleAgentRoleRelease(ctx, relReq2)
	require.NoError(t, err)
	require.False(t, res.IsError)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(res.Content[0].(mcpgo.TextContent).Text), &out))
	assert.Equal(t, true, out["redundant"], "second release of the same grant must report redundant=true")

	// Session row stays cleared (the first release already cleared it;
	// the second is a no-op).
	sess, err := s.sessions.Get(ctx, "u1", "sess_a")
	require.NoError(t, err)
	assert.Empty(t, sess.OrchestratorGrantID)
}

// Compile-time guard: rolegrant statuses are referenced by name in the
// gating logic; this assignment fails to compile if the constant is
// renamed without updating the handler.
var _ = rolegrant.StatusActive
