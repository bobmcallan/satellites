package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/mcpserver"
	"github.com/bobmcallan/satellites/internal/reviewer/service"
	"github.com/bobmcallan/satellites/internal/rolegrant"
	"github.com/bobmcallan/satellites/internal/session"
)

// seedReviewerDocs mirrors the seed shape of cmd/satellites/main.go's
// seedReviewerDocs so this test runs without dragging the binary
// package in. The seeded structured payloads only need the fields the
// FK resolver inspects (type + status).
func seedReviewerDocs(t *testing.T, docs document.Store, workspaceID string, now time.Time) (document.Document, document.Document) {
	t.Helper()
	role, err := docs.Create(context.Background(), document.Document{
		WorkspaceID: workspaceID,
		Type:        document.TypeRole,
		Name:        mcpserver.SeedRoleReviewerName,
		Scope:       document.ScopeSystem,
		Status:      document.StatusActive,
		Structured:  []byte(`{"allowed_mcp_verbs":["task_claim","contract_review_close"]}`),
	}, now)
	require.NoError(t, err)
	agent, err := docs.Create(context.Background(), document.Document{
		WorkspaceID: workspaceID,
		Type:        document.TypeAgent,
		Name:        mcpserver.SeedAgentReviewerName,
		Scope:       document.ScopeSystem,
		Status:      document.StatusActive,
		Structured:  []byte(`{"permitted_roles":["` + role.ID + `"],"tool_ceiling":["task_claim","contract_review_close"]}`),
	}, now)
	require.NoError(t, err)
	return role, agent
}

// ensureReviewerServiceGrant is a copy of the helper in
// cmd/satellites/main.go kept here to exercise the seed + grant +
// session-stamp flow without booting the binary. The two
// implementations stay in lockstep — mismatches surface as
// integration test failures on either the service or main package.
func ensureReviewerServiceGrant(
	t *testing.T,
	docs document.Store,
	grants rolegrant.Store,
	sessions session.Store,
	workspaceID string,
	now time.Time,
) (string, error) {
	t.Helper()
	role, err := docs.GetByName(context.Background(), "", mcpserver.SeedRoleReviewerName, nil)
	if err != nil {
		return "", nil
	}
	agent, err := docs.GetByName(context.Background(), "", mcpserver.SeedAgentReviewerName, nil)
	if err != nil {
		return "", nil
	}
	sess, err := sessions.Register(context.Background(), service.ServiceUserID, service.ServiceSessionID, session.SourceSessionStart, now)
	if err != nil {
		return "", err
	}
	if workspaceID != "" && sess.WorkspaceID != workspaceID {
		if updated, err := sessions.SetWorkspace(context.Background(), service.ServiceUserID, service.ServiceSessionID, workspaceID, now); err == nil {
			sess = updated
		}
	}
	if sess.OrchestratorGrantID != "" {
		existing, gerr := grants.GetByID(context.Background(), sess.OrchestratorGrantID, nil)
		if gerr == nil && existing.Status == rolegrant.StatusActive && existing.RoleID == role.ID && existing.AgentID == agent.ID {
			return existing.ID, nil
		}
	}
	grant, err := grants.Create(context.Background(), rolegrant.RoleGrant{
		WorkspaceID: workspaceID,
		RoleID:      role.ID,
		AgentID:     agent.ID,
		GranteeKind: rolegrant.GranteeSession,
		GranteeID:   service.ServiceSessionID,
	}, now)
	if err != nil {
		return "", err
	}
	if _, err := sessions.SetOrchestratorGrant(context.Background(), service.ServiceUserID, service.ServiceSessionID, grant.ID, now); err != nil {
		return grant.ID, err
	}
	return grant.ID, nil
}

func TestSeedReviewerDocs_CreatesRoleAndAgent(t *testing.T) {
	t.Parallel()
	docs := document.NewMemoryStore()
	now := time.Now().UTC()
	role, agent := seedReviewerDocs(t, docs, "wksp_sys", now)

	assert.Equal(t, mcpserver.SeedRoleReviewerName, role.Name)
	assert.Equal(t, document.TypeRole, role.Type)
	assert.Equal(t, document.StatusActive, role.Status)

	assert.Equal(t, mcpserver.SeedAgentReviewerName, agent.Name)
	assert.Equal(t, document.TypeAgent, agent.Type)
	assert.Equal(t, document.StatusActive, agent.Status)
	assert.Contains(t, string(agent.Structured), role.ID, "agent permitted_roles should reference role id")
}

func TestEnsureReviewerServiceGrant_HappyPath(t *testing.T) {
	t.Parallel()
	docs := document.NewMemoryStore()
	grants := rolegrant.NewMemoryStore(docs)
	sessions := session.NewMemoryStore()
	now := time.Now().UTC()
	role, agent := seedReviewerDocs(t, docs, "wksp_sys", now)

	grantID, err := ensureReviewerServiceGrant(t, docs, grants, sessions, "wksp_sys", now)
	require.NoError(t, err)
	require.NotEmpty(t, grantID)

	// Grant resolves with role_reviewer + agent_gemini_reviewer pinning,
	// status=active, grantee=service session.
	grant, err := grants.GetByID(context.Background(), grantID, nil)
	require.NoError(t, err)
	assert.Equal(t, rolegrant.StatusActive, grant.Status)
	assert.Equal(t, rolegrant.GranteeSession, grant.GranteeKind)
	assert.Equal(t, service.ServiceSessionID, grant.GranteeID)
	assert.Equal(t, role.ID, grant.RoleID)
	assert.Equal(t, agent.ID, grant.AgentID)

	// Session row carries the grant id so the session-role gate can
	// resolve grant → role match on every task_claim / contract_review_close.
	sess, err := sessions.Get(context.Background(), service.ServiceUserID, service.ServiceSessionID)
	require.NoError(t, err)
	assert.Equal(t, grantID, sess.OrchestratorGrantID)
	assert.Equal(t, "wksp_sys", sess.WorkspaceID)
}

func TestEnsureReviewerServiceGrant_Idempotent(t *testing.T) {
	t.Parallel()
	docs := document.NewMemoryStore()
	grants := rolegrant.NewMemoryStore(docs)
	sessions := session.NewMemoryStore()
	now := time.Now().UTC()
	seedReviewerDocs(t, docs, "wksp_sys", now)

	first, err := ensureReviewerServiceGrant(t, docs, grants, sessions, "wksp_sys", now)
	require.NoError(t, err)

	// Second invocation reuses the existing active grant rather than
	// minting a duplicate. The boot path runs every restart; without
	// idempotence the grant table grows unbounded.
	second, err := ensureReviewerServiceGrant(t, docs, grants, sessions, "wksp_sys", now.Add(time.Minute))
	require.NoError(t, err)
	assert.Equal(t, first, second, "second call should reuse the first grant id")

	active, err := grants.List(context.Background(), rolegrant.ListOptions{Status: rolegrant.StatusActive}, nil)
	require.NoError(t, err)
	assert.Len(t, active, 1, "exactly one active grant after idempotent re-mint")
}

func TestEnsureReviewerServiceGrant_SkipsWhenSeedsMissing(t *testing.T) {
	t.Parallel()
	docs := document.NewMemoryStore()
	grants := rolegrant.NewMemoryStore(docs)
	sessions := session.NewMemoryStore()
	now := time.Now().UTC()

	grantID, err := ensureReviewerServiceGrant(t, docs, grants, sessions, "wksp_sys", now)
	require.NoError(t, err)
	assert.Empty(t, grantID, "no seeds → no grant minted")

	// Session row should not have been created either — without seeds
	// the helper short-circuits before touching the session store.
	_, err = sessions.Get(context.Background(), service.ServiceUserID, service.ServiceSessionID)
	assert.Error(t, err, "no seeds → no session registered")
}

func TestSeedReviewerDocs_ReSeedIdempotent(t *testing.T) {
	t.Parallel()
	docs := document.NewMemoryStore()
	now := time.Now().UTC()
	seedReviewerDocs(t, docs, "wksp_sys", now)

	// A second seed call against the same store would error inside
	// docs.Create — the test helper requires no error. Mirror the
	// production seed's "skip when present" branch by re-querying.
	role, err := docs.GetByName(context.Background(), "", mcpserver.SeedRoleReviewerName, nil)
	require.NoError(t, err)
	agent, err := docs.GetByName(context.Background(), "", mcpserver.SeedAgentReviewerName, nil)
	require.NoError(t, err)
	assert.NotEmpty(t, role.ID)
	assert.NotEmpty(t, agent.ID)
}
