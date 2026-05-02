package mcpserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/rolegrant"
	"github.com/bobmcallan/satellites/internal/session"
)

// orchestratorTestServer mirrors the boot wiring a narrow subset of
// Server needs for the SessionStart grant path: docs store (seeded with
// role_orchestrator + agent_claude_orchestrator), grants store, session
// store, ledger store. cfg.GrantsEnforced stays false (6.5 flip).
func orchestratorTestServer(t *testing.T, seed bool) *Server {
	t.Helper()
	docs := document.NewMemoryStore()
	grants := rolegrant.NewMemoryStore(docs)
	sessions := session.NewMemoryStore()
	ldgr := ledger.NewMemoryStore()
	s := &Server{
		cfg:      &config.Config{GrantsEnforced: false},
		docs:     docs,
		grants:   grants,
		sessions: sessions,
		ledger:   ldgr,
	}
	if seed {
		ctx := context.Background()
		now := time.Now().UTC()
		role, err := docs.Create(ctx, document.Document{
			WorkspaceID: "wksp_sys",
			Type:        document.TypeRole,
			Name:        SeedRoleOrchestratorName,
			Scope:       document.ScopeSystem,
			Status:      document.StatusActive,
			Structured:  []byte(`{"allowed_mcp_verbs":["document_*","story_*"],"required_hooks":["SessionStart"]}`),
		}, now)
		require.NoError(t, err)
		_, err = docs.Create(ctx, document.Document{
			WorkspaceID: "wksp_sys",
			Type:        document.TypeAgent,
			Name:        SeedAgentOrchestratorName,
			Scope:       document.ScopeSystem,
			Status:      document.StatusActive,
			Structured:  []byte(`{"permitted_roles":["` + role.ID + `"],"tool_ceiling":["*"]}`),
		}, now)
		require.NoError(t, err)
	}
	return s
}

func callRegister(t *testing.T, s *Server, userID, sessionID string) map[string]any {
	t.Helper()
	ctx := context.WithValue(context.Background(), userKey, CallerIdentity{UserID: userID, Email: userID + "@example.com", Source: "apikey"})
	req := mcpgo.CallToolRequest{}
	req.Params.Arguments = map[string]any{"session_id": sessionID, "source": session.SourceSessionStart}
	res, err := s.handleSessionRegister(ctx, req)
	require.NoError(t, err)
	require.False(t, res.IsError, "register error: %+v", res)
	text := res.Content[0].(mcpgo.TextContent).Text
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &out))
	return out
}

// TestSessionRegister_NoAutoGrant covers epic:agent-process-v1
// (sty_a4074d21): session_register no longer mints an orchestrator
// grant. The session row carries an empty OrchestratorGrantID until
// agent_role_claim is called explicitly.
func TestSessionRegister_NoAutoGrant(t *testing.T) {
	t.Parallel()
	s := orchestratorTestServer(t, true)
	out := callRegister(t, s, "u1", "sess_aaa")
	if gid, _ := out["orchestrator_grant_id"].(string); gid != "" {
		t.Fatalf("session_register must not mint an orchestrator grant; got %q", gid)
	}

	// Session row exists with empty OrchestratorGrantID; no grants in the
	// store yet.
	sess, err := s.sessions.Get(context.Background(), "u1", "sess_aaa")
	require.NoError(t, err)
	assert.Equal(t, "sess_aaa", sess.SessionID)
	assert.Empty(t, sess.OrchestratorGrantID)
	active, err := s.grants.List(context.Background(), rolegrant.ListOptions{Status: rolegrant.StatusActive}, nil)
	require.NoError(t, err)
	assert.Len(t, active, 0, "no auto-grants minted")
}

// TestSessionRegister_NoAutoGrant_SeedsAbsent confirms the no-auto-grant
// behaviour holds whether or not the orchestrator seeds resolve.
func TestSessionRegister_NoAutoGrant_SeedsAbsent(t *testing.T) {
	t.Parallel()
	s := orchestratorTestServer(t, false)
	out := callRegister(t, s, "u1", "sess_noseed")
	if gid, _ := out["orchestrator_grant_id"].(string); gid != "" {
		t.Fatalf("no seeds → no grant; got %q", gid)
	}

	sess, err := s.sessions.Get(context.Background(), "u1", "sess_noseed")
	require.NoError(t, err)
	assert.Equal(t, "sess_noseed", sess.SessionID)
	assert.Empty(t, sess.OrchestratorGrantID)
}

// TestAgentRoleClaim_StampsGrantOnSession covers the post-unblock
// (sty_a4074d21) flow: agent_role_claim with grantee_kind=session
// stamps the new grant id on the caller's session row so the
// contract_claim required_role gate finds it. Replaces the dropped
// session_register auto-grant path.
func TestAgentRoleClaim_StampsGrantOnSession(t *testing.T) {
	t.Parallel()
	s := orchestratorTestServer(t, true)
	callRegister(t, s, "u1", "sess_claim")

	roleDoc, err := s.docs.GetByName(context.Background(), "", SeedRoleOrchestratorName, nil)
	require.NoError(t, err)
	agentDoc, err := s.docs.GetByName(context.Background(), "", SeedAgentOrchestratorName, nil)
	require.NoError(t, err)

	ctx := context.WithValue(context.Background(), userKey, CallerIdentity{UserID: "u1", Email: "u1@example.com", Source: "apikey"})
	req := mcpgo.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"workspace_id": "wksp_sys",
		"role_id":      roleDoc.ID,
		"agent_id":     agentDoc.ID,
		"grantee_kind": "session",
		"grantee_id":   "sess_claim",
	}
	res, err := s.handleAgentRoleClaim(ctx, req)
	require.NoError(t, err)
	require.False(t, res.IsError, "claim error: %+v", res)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(res.Content[0].(mcpgo.TextContent).Text), &out))
	grantID, _ := out["grant_id"].(string)
	require.NotEmpty(t, grantID)

	// Session row is stamped with the same grant id so the gate finds it.
	sess, err := s.sessions.Get(context.Background(), "u1", "sess_claim")
	require.NoError(t, err)
	assert.Equal(t, grantID, sess.OrchestratorGrantID, "agent_role_claim must stamp the grant id on the session row")
}

func TestSessionWhoami_ReturnsOrchestratorGrant(t *testing.T) {
	t.Parallel()
	s := orchestratorTestServer(t, true)
	callRegister(t, s, "u1", "sess_whoami")

	// Explicit role claim — replaces the auto-grant path.
	roleDoc, err := s.docs.GetByName(context.Background(), "", SeedRoleOrchestratorName, nil)
	require.NoError(t, err)
	agentDoc, err := s.docs.GetByName(context.Background(), "", SeedAgentOrchestratorName, nil)
	require.NoError(t, err)
	claimReq := mcpgo.CallToolRequest{}
	claimReq.Params.Arguments = map[string]any{
		"workspace_id": "wksp_sys",
		"role_id":      roleDoc.ID,
		"agent_id":     agentDoc.ID,
		"grantee_kind": "session",
		"grantee_id":   "sess_whoami",
	}
	claimCtx := context.WithValue(context.Background(), userKey, CallerIdentity{UserID: "u1", Email: "u1@example.com", Source: "apikey"})
	claimRes, err := s.handleAgentRoleClaim(claimCtx, claimReq)
	require.NoError(t, err)
	require.False(t, claimRes.IsError)

	ctx := context.WithValue(context.Background(), userKey, CallerIdentity{UserID: "u1", Email: "u1@example.com", Source: "apikey"})
	req := mcpgo.CallToolRequest{}
	req.Params.Arguments = map[string]any{"session_id": "sess_whoami"}
	res, err := s.handleSessionWhoami(ctx, req)
	require.NoError(t, err)
	require.False(t, res.IsError)
	text := res.Content[0].(mcpgo.TextContent).Text
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &out))
	grantID, _ := out["orchestrator_grant_id"].(string)
	assert.NotEmpty(t, grantID, "whoami should include orchestrator_grant_id after explicit role claim")
	verbs, _ := out["effective_verbs"].([]any)
	assert.NotEmpty(t, verbs, "whoami should include effective_verbs when grant is live")
}
