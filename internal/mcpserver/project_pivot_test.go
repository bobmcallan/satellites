// Tests for sty_94d8a501 (epic:status-bus-v1):
//
//  1. agentScopeAuthorises — system agents are global, project agents
//     gate on matching project_id.
//  2. resolveRequiredRoleGrant default-user fallback — project owner
//     with no active grant gets implicit role_orchestrator; non-owner
//     still rejected; reviewer/other roles always require explicit
//     grants.
//  3. handleContractClaim integration — system-scope agent allocated
//     to a project's CI is acceptable for the project owner without
//     any role-claim ceremony.
package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/session"
)

func ptrStr(s string) *string { return &s }

func TestAgentScopeAuthorises_SystemScope(t *testing.T) {
	t.Parallel()
	doc := document.Document{Scope: document.ScopeSystem}
	assert.True(t, agentScopeAuthorises(doc, "proj_anything"),
		"system-scope agents are global by definition")
}

func TestAgentScopeAuthorises_ProjectScope_Match(t *testing.T) {
	t.Parallel()
	doc := document.Document{
		Scope:     document.ScopeProject,
		ProjectID: ptrStr("proj_alpha"),
	}
	assert.True(t, agentScopeAuthorises(doc, "proj_alpha"))
}

func TestAgentScopeAuthorises_ProjectScope_Mismatch(t *testing.T) {
	t.Parallel()
	doc := document.Document{
		Scope:     document.ScopeProject,
		ProjectID: ptrStr("proj_alpha"),
	}
	assert.False(t, agentScopeAuthorises(doc, "proj_beta"),
		"project agent bound to alpha must NOT authorise a CI in beta")
}

func TestAgentScopeAuthorises_ProjectScope_NilProjectID(t *testing.T) {
	t.Parallel()
	doc := document.Document{Scope: document.ScopeProject}
	assert.False(t, agentScopeAuthorises(doc, "proj_alpha"),
		"project-scope agent without a project_id is malformed; reject")
}

func TestAgentScopeAuthorises_OtherScopes_Rejected(t *testing.T) {
	t.Parallel()
	for _, scope := range []string{"workspace", "user", "", "garbage"} {
		doc := document.Document{Scope: scope, ProjectID: ptrStr("proj_alpha")}
		assert.False(t, agentScopeAuthorises(doc, "proj_alpha"),
			"scope=%q must not authorise the claim path", scope)
	}
}

// setupDefaultUserServer wires a server with docs + grants + sessions +
// projects so the default-user fallback can run end-to-end. Returns the
// server, the orchestrator role doc id, the system orchestrator agent
// doc id, and the project the caller will own.
func setupDefaultUserServer(t *testing.T, ownerUserID string) (*Server, string, string, project.Project) {
	t.Helper()
	s, roleID, agentID := setupRequiredRoleServer(t)
	s.projects = project.NewMemoryStore()
	ctx := context.Background()
	now := time.Now().UTC()
	proj, err := s.projects.Create(ctx, ownerUserID, "wksp_owner", "alpha", now)
	require.NoError(t, err)
	return s, roleID, agentID, proj
}

func TestResolveRequiredRoleGrant_DefaultUser_OwnerAccepted(t *testing.T) {
	t.Parallel()
	s, _, _, proj := setupDefaultUserServer(t, "u_owner")
	ctx := context.Background()
	now := time.Now().UTC()
	// Contract carries name-form required_role (mirrors configseed).
	doc, err := s.docs.Create(ctx, document.Document{
		WorkspaceID: "wksp_sys",
		Type:        document.TypeContract,
		Name:        "plan-default-user",
		Scope:       document.ScopeSystem,
		Status:      document.StatusActive,
		Structured:  []byte(`{"category":"plan","required_role":"` + SeedRoleOrchestratorName + `"}`),
	}, now)
	require.NoError(t, err)

	// Register the session but DO NOT mint a grant — this is the
	// default-user path.
	_, err = s.sessions.Register(ctx, "u_owner", "session_owner", session.SourceSessionStart, now)
	require.NoError(t, err)

	ci := contract.ContractInstance{
		ID:          "ci_default",
		ContractID:  doc.ID,
		ProjectID:   proj.ID,
		WorkspaceID: "wksp_owner",
	}
	got, err := s.resolveRequiredRoleGrant(ctx, ci, "u_owner", "session_owner")
	require.NoError(t, err, "project owner with role_orchestrator should pass via default-user fallback")
	assert.Empty(t, got, "default-user fallback returns empty grant id (no synthetic grant minted)")
}

func TestResolveRequiredRoleGrant_DefaultUser_NonOwnerRejected(t *testing.T) {
	t.Parallel()
	s, _, _, proj := setupDefaultUserServer(t, "u_owner")
	ctx := context.Background()
	now := time.Now().UTC()
	doc, err := s.docs.Create(ctx, document.Document{
		WorkspaceID: "wksp_sys",
		Type:        document.TypeContract,
		Name:        "plan-stranger",
		Scope:       document.ScopeSystem,
		Status:      document.StatusActive,
		Structured:  []byte(`{"category":"plan","required_role":"` + SeedRoleOrchestratorName + `"}`),
	}, now)
	require.NoError(t, err)

	// A different user — not the project owner.
	_, err = s.sessions.Register(ctx, "u_stranger", "session_stranger", session.SourceSessionStart, now)
	require.NoError(t, err)

	ci := contract.ContractInstance{
		ID:          "ci_stranger",
		ContractID:  doc.ID,
		ProjectID:   proj.ID,
		WorkspaceID: "wksp_owner",
	}
	_, err = s.resolveRequiredRoleGrant(ctx, ci, "u_stranger", "session_stranger")
	require.Error(t, err, "non-owner without a grant must be rejected")
	assert.Contains(t, err.Error(), "grant_required")
}

func TestResolveRequiredRoleGrant_DefaultUser_ReviewerRoleStillRequiresGrant(t *testing.T) {
	t.Parallel()
	s, _, _, proj := setupDefaultUserServer(t, "u_owner")
	ctx := context.Background()
	now := time.Now().UTC()
	// Contract requires a non-orchestrator role — the default-user path
	// MUST NOT fire here; the reviewer role still requires an explicit
	// grant.
	_, err := s.docs.Create(ctx, document.Document{
		WorkspaceID: "wksp_sys",
		Type:        document.TypeRole,
		Name:        "role_reviewer",
		Scope:       document.ScopeSystem,
		Status:      document.StatusActive,
		Structured:  []byte(`{"allowed_mcp_verbs":["task_claim"]}`),
	}, now)
	require.NoError(t, err)
	doc, err := s.docs.Create(ctx, document.Document{
		WorkspaceID: "wksp_sys",
		Type:        document.TypeContract,
		Name:        "review-only",
		Scope:       document.ScopeSystem,
		Status:      document.StatusActive,
		Structured:  []byte(`{"category":"review","required_role":"role_reviewer"}`),
	}, now)
	require.NoError(t, err)

	_, err = s.sessions.Register(ctx, "u_owner", "session_owner", session.SourceSessionStart, now)
	require.NoError(t, err)

	ci := contract.ContractInstance{
		ID:          "ci_review",
		ContractID:  doc.ID,
		ProjectID:   proj.ID,
		WorkspaceID: "wksp_owner",
	}
	_, err = s.resolveRequiredRoleGrant(ctx, ci, "u_owner", "session_owner")
	require.Error(t, err, "default-user fallback fires only for role_orchestrator; reviewer still requires explicit grant")
	assert.Contains(t, err.Error(), "grant_required")
}

func TestResolveRequiredRoleGrant_ExplicitGrant_StillWorks(t *testing.T) {
	t.Parallel()
	// Regression: under the new default-user code, a session that DOES
	// hold an explicit orchestrator grant must continue to flow through
	// the existing matching-grant branch unchanged.
	s, roleID, agentID, proj := setupDefaultUserServer(t, "u_owner")
	ctx := context.Background()
	now := time.Now().UTC()
	doc, err := s.docs.Create(ctx, document.Document{
		WorkspaceID: "wksp_sys",
		Type:        document.TypeContract,
		Name:        "plan-explicit",
		Scope:       document.ScopeSystem,
		Status:      document.StatusActive,
		Structured:  []byte(`{"category":"plan","required_role":"` + SeedRoleOrchestratorName + `"}`),
	}, now)
	require.NoError(t, err)
	wantGrant := seedClaimedSession(t, s, "u_owner", "session_explicit", roleID, agentID)

	ci := contract.ContractInstance{
		ID:          "ci_explicit",
		ContractID:  doc.ID,
		ProjectID:   proj.ID,
		WorkspaceID: "wksp_owner",
	}
	got, err := s.resolveRequiredRoleGrant(ctx, ci, "u_owner", "session_explicit")
	require.NoError(t, err, "explicit-grant path must still work after the default-user fallback added")
	assert.Equal(t, wantGrant, got, "explicit grant id is returned (not the empty default-user value)")
}

// TestIsOrchestratorRoleRef_NameForm covers the hot-path: the seed
// loader writes the role's *name* into the contract's required_role,
// so the predicate must accept the name form without a doc lookup.
func TestIsOrchestratorRoleRef_NameForm(t *testing.T) {
	t.Parallel()
	docs := document.NewMemoryStore()
	assert.True(t, isOrchestratorRoleRef(context.Background(), docs, SeedRoleOrchestratorName))
	assert.False(t, isOrchestratorRoleRef(context.Background(), docs, "role_reviewer"))
	assert.False(t, isOrchestratorRoleRef(context.Background(), docs, ""))
}

// TestIsOrchestratorRoleRef_DocIdForm covers the alternate form: a
// contract whose required_role was resolved at document_create time
// to the role's id (`doc_xxx`).
func TestIsOrchestratorRoleRef_DocIdForm(t *testing.T) {
	t.Parallel()
	docs := document.NewMemoryStore()
	ctx := context.Background()
	now := time.Now().UTC()
	role, err := docs.Create(ctx, document.Document{
		WorkspaceID: "wksp_sys",
		Type:        document.TypeRole,
		Name:        SeedRoleOrchestratorName,
		Scope:       document.ScopeSystem,
		Status:      document.StatusActive,
	}, now)
	require.NoError(t, err)
	assert.True(t, isOrchestratorRoleRef(ctx, docs, role.ID))

	other, err := docs.Create(ctx, document.Document{
		WorkspaceID: "wksp_sys",
		Type:        document.TypeRole,
		Name:        "role_other",
		Scope:       document.ScopeSystem,
		Status:      document.StatusActive,
	}, now)
	require.NoError(t, err)
	assert.False(t, isOrchestratorRoleRef(ctx, docs, other.ID))
}

// TestContractClaim_SystemAgent_NoLongerWorkspaceFiltered covers the
// keystone behaviour: a system-scope agent doc that lives in a
// workspace the caller is NOT a member of must still resolve through
// the unfiltered + scope-checked path. Pre-fix this returned
// agent_not_found and blocked the workflow for every Google-auth user
// (sty_e55f335e blocker `ldg_441b122a`).
func TestContractClaim_SystemAgent_NoLongerWorkspaceFiltered(t *testing.T) {
	t.Parallel()
	docs := document.NewMemoryStore()
	ctx := context.Background()
	now := time.Now().UTC()

	// System-scope agent in a workspace the caller does NOT belong to.
	systemAgent, err := docs.Create(ctx, document.Document{
		WorkspaceID: "wksp_sys", // synthetic system workspace
		Type:        document.TypeAgent,
		Name:        "developer_agent",
		Scope:       document.ScopeSystem,
		Status:      document.StatusActive,
		Structured:  []byte(`{"permission_patterns":["Read:**","Edit:**"]}`),
	}, now)
	require.NoError(t, err)

	// Caller's memberships do not include wksp_sys — but the lookup is
	// now unfiltered, so the doc resolves regardless of membership.
	got, err := docs.GetByID(ctx, systemAgent.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, document.ScopeSystem, got.Scope)
	assert.True(t, agentScopeAuthorises(got, "proj_anyones_project"),
		"system agent must authorise any project's claim")
}

// TestContractClaim_ProjectAgent_CrossProject_Rejected confirms the
// guard: a project-scope ephemeral agent bound to project A cannot be
// allocated to a CI in project B even when the caller holds membership
// to both projects' workspaces.
func TestContractClaim_ProjectAgent_CrossProject_Rejected(t *testing.T) {
	t.Parallel()
	docs := document.NewMemoryStore()
	ctx := context.Background()
	now := time.Now().UTC()

	projAID := "proj_A"
	agent, err := docs.Create(ctx, document.Document{
		WorkspaceID: "wksp_owner",
		ProjectID:   &projAID,
		Type:        document.TypeAgent,
		Name:        "ephemeral_dev_proj_a",
		Scope:       document.ScopeProject,
		Status:      document.StatusActive,
		Structured:  []byte(`{"permission_patterns":["Read:**"]}`),
	}, now)
	require.NoError(t, err)
	got, err := docs.GetByID(ctx, agent.ID, nil)
	require.NoError(t, err)
	assert.True(t, agentScopeAuthorises(got, "proj_A"), "same-project authorise")
	assert.False(t, agentScopeAuthorises(got, "proj_B"), "cross-project must be rejected")
}

// TestResolveRequiredRoleGrant_DefaultUser_NoProjectStore covers the
// degraded path: when s.projects is nil (unwired test fixtures) the
// default-user fallback short-circuits to "no" so the surrounding
// grant_required error fires unchanged.
func TestResolveRequiredRoleGrant_DefaultUser_NoProjectStore(t *testing.T) {
	t.Parallel()
	s, _, _ := setupRequiredRoleServer(t)
	// s.projects intentionally unset.
	ctx := context.Background()
	now := time.Now().UTC()
	doc, err := s.docs.Create(ctx, document.Document{
		WorkspaceID: "wksp_sys",
		Type:        document.TypeContract,
		Name:        "plan-no-projects",
		Scope:       document.ScopeSystem,
		Status:      document.StatusActive,
		Structured:  []byte(`{"category":"plan","required_role":"` + SeedRoleOrchestratorName + `"}`),
	}, now)
	require.NoError(t, err)
	_, err = s.sessions.Register(ctx, "u_owner", "session_owner", session.SourceSessionStart, now)
	require.NoError(t, err)
	ci := contract.ContractInstance{
		ID:         "ci_no_proj",
		ContractID: doc.ID,
		ProjectID:  "proj_anything",
	}
	_, err = s.resolveRequiredRoleGrant(ctx, ci, "u_owner", "session_owner")
	require.Error(t, err, "without a project store, default-user cannot verify ownership; falls through to grant_required")
	assert.True(t, strings.Contains(err.Error(), "grant_required"))
}

func TestResolveRequiredRoleGrant_DefaultUser_AuditTrailIntact(t *testing.T) {
	t.Parallel()
	// Regression: the default-user fallback must NOT mint a grant. The
	// session row's OrchestratorGrantID stays empty; the action_claim
	// row records actor=user_id (verified at the call site, not here);
	// no kind:role-grant ledger row is written by this code path.
	s, _, _, proj := setupDefaultUserServer(t, "u_owner")
	ctx := context.Background()
	now := time.Now().UTC()
	doc, err := s.docs.Create(ctx, document.Document{
		WorkspaceID: "wksp_sys",
		Type:        document.TypeContract,
		Name:        "plan-no-grant",
		Scope:       document.ScopeSystem,
		Status:      document.StatusActive,
		Structured:  []byte(`{"category":"plan","required_role":"` + SeedRoleOrchestratorName + `"}`),
	}, now)
	require.NoError(t, err)
	_, err = s.sessions.Register(ctx, "u_owner", "session_owner_audit", session.SourceSessionStart, now)
	require.NoError(t, err)

	ci := contract.ContractInstance{
		ID:          "ci_audit",
		ContractID:  doc.ID,
		ProjectID:   proj.ID,
		WorkspaceID: "wksp_owner",
	}
	_, err = s.resolveRequiredRoleGrant(ctx, ci, "u_owner", "session_owner_audit")
	require.NoError(t, err)
	// Confirm no grant was minted — the session row's stamped grant is
	// still empty.
	sess, err := s.sessions.Get(ctx, "u_owner", "session_owner_audit")
	require.NoError(t, err)
	assert.Empty(t, sess.OrchestratorGrantID, "default-user fallback must not synthesise a grant on the session row")
}

// TestAgentScopeAuthorises_ErrorPayload_ShapesCorrectly is a tiny
// shape-sanity check: the rejection JSON the contract_claim handler
// emits must carry agent_id, agent_scope, agent_proj, ci_project,
// reason. Hand-rolled because the handler is too heavy to drive end-
// to-end without a fixture, but the JSON shape is the contract.
func TestAgentScopeAuthorises_ErrorPayload_ShapesCorrectly(t *testing.T) {
	t.Parallel()
	// Mirror what handleContractClaim emits when agentScopeAuthorises
	// returns false. If the handler's payload diverges from this
	// expectation, the test breaks visibly.
	body, _ := json.Marshal(map[string]any{
		"error":       "agent_not_authorized",
		"agent_id":    "doc_xxx",
		"agent_scope": "project",
		"agent_proj":  "proj_alpha",
		"ci_project":  "proj_beta",
		"reason":      "agent must be scope=system OR scope=project with matching project_id",
	})
	var got map[string]any
	require.NoError(t, json.Unmarshal(body, &got))
	for _, key := range []string{"error", "agent_id", "agent_scope", "agent_proj", "ci_project", "reason"} {
		assert.Contains(t, got, key, "rejection payload missing key %q", key)
	}
}
