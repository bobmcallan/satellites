package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/rolegrant"
)

// mechanicalTestServer wires the Server subset needed to exercise
// handleAgentRoleClaim's mechanical fallback paths.
func mechanicalTestServer(t *testing.T, seedRole bool) *Server {
	t.Helper()
	docs := document.NewMemoryStore()
	grants := rolegrant.NewMemoryStore(docs)
	ldgr := ledger.NewMemoryStore()
	s := &Server{
		cfg:    &config.Config{GrantsEnforced: false},
		docs:   docs,
		grants: grants,
		ledger: ldgr,
	}
	if seedRole {
		_, err := docs.Create(context.Background(), document.Document{
			WorkspaceID: "wksp_a",
			Type:        document.TypeRole,
			Name:        "role_orchestrator",
			Scope:       document.ScopeSystem,
			Status:      document.StatusActive,
			Structured:  []byte(`{"allowed_mcp_verbs":["document_get"]}`),
		}, time.Now().UTC())
		require.NoError(t, err)
	}
	return s
}

func callAgentRoleClaim(t *testing.T, s *Server, args map[string]any) map[string]any {
	t.Helper()
	req := mcpgo.CallToolRequest{}
	req.Params.Arguments = args
	res, err := s.handleAgentRoleClaim(context.Background(), req)
	require.NoError(t, err)
	require.Greater(t, len(res.Content), 0)
	text := res.Content[0].(mcpgo.TextContent).Text
	require.False(t, res.IsError, "handler returned error: %s", text)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &out))
	return out
}

func TestAgentRoleClaim_MechanicalOverride_EmitsProviderMechanical(t *testing.T) {
	t.Parallel()
	s := mechanicalTestServer(t, true)
	// Find the seeded role id.
	role, err := s.docs.GetByName(context.Background(), "", "role_orchestrator", nil)
	require.NoError(t, err)

	out := callAgentRoleClaim(t, s, map[string]any{
		"workspace_id":      "wksp_a",
		"role_id":           role.ID,
		"agent_id":          "",
		"grantee_kind":      "session",
		"grantee_id":        "sess_force",
		"provider_override": "mechanical",
	})
	assert.Equal(t, "mechanical", out["provider"])
	assert.Equal(t, "explicit-force", out["trigger_reason"])
	assert.Equal(t, "active", out["status"])

	// Verify a ledger row with provider:mechanical was written.
	rows, err := s.ledger.List(context.Background(), "", ledger.ListOptions{}, nil)
	require.NoError(t, err)
	found := false
	for _, r := range rows {
		if containsTag(r.Tags, "provider:mechanical") && containsTag(r.Tags, "reason:explicit-force") {
			found = true
		}
	}
	assert.True(t, found, "expected a ledger row tagged provider:mechanical,reason:explicit-force; got %d rows", len(rows))
}

func TestAgentRoleClaim_UnresolvedAgent_FallsThroughToMechanical(t *testing.T) {
	t.Parallel()
	s := mechanicalTestServer(t, true)
	role, err := s.docs.GetByName(context.Background(), "", "role_orchestrator", nil)
	require.NoError(t, err)

	// agent_id points at a document that doesn't exist.
	out := callAgentRoleClaim(t, s, map[string]any{
		"workspace_id": "wksp_a",
		"role_id":      role.ID,
		"agent_id":     "doc_does_not_exist",
		"grantee_kind": "session",
		"grantee_id":   "sess_noagent",
	})
	assert.Equal(t, "mechanical", out["provider"])
	assert.Equal(t, "no-agent-resolved", out["trigger_reason"])
}

func TestAgentRoleClaim_NoAgentID_FallsThroughToMechanical(t *testing.T) {
	t.Parallel()
	s := mechanicalTestServer(t, true)
	role, err := s.docs.GetByName(context.Background(), "", "role_orchestrator", nil)
	require.NoError(t, err)

	// agent_id omitted entirely. Without provider_override this should
	// still route through the mechanical path.
	out := callAgentRoleClaim(t, s, map[string]any{
		"workspace_id": "wksp_a",
		"role_id":      role.ID,
		"grantee_kind": "session",
		"grantee_id":   "sess_noid",
	})
	assert.Equal(t, "mechanical", out["provider"])
	assert.Equal(t, "no-agent-resolved", out["trigger_reason"])
}

func TestAgentRoleClaim_EffectiveVerbsSynthesizedFromRole(t *testing.T) {
	t.Parallel()
	s := mechanicalTestServer(t, true)
	role, err := s.docs.GetByName(context.Background(), "", "role_orchestrator", nil)
	require.NoError(t, err)

	out := callAgentRoleClaim(t, s, map[string]any{
		"workspace_id":      "wksp_a",
		"role_id":           role.ID,
		"grantee_kind":      "session",
		"grantee_id":        "sess_verbs",
		"provider_override": "mechanical",
	})
	verbs, _ := out["effective_verbs"].([]any)
	require.NotEmpty(t, verbs)
	assert.Equal(t, "document_get", verbs[0], "mechanical path should surface role.allowed_mcp_verbs as effective_verbs")
}

func containsTag(tags []string, want string) bool {
	for _, t := range tags {
		if strings.EqualFold(t, want) {
			return true
		}
	}
	return false
}
