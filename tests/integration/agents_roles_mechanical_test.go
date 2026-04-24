package integration

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/moby/moby/api/types/mount"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestAgentsRolesMechanical_ProviderOverride_EndToEnd boots the full
// container stack and exercises story_548ab5a5's mechanical-fallback
// tier via the MCP surface.
//
//  1. agent_role_claim with provider_override="mechanical" returns a
//     grant response carrying provider=mechanical and trigger_reason=
//     explicit-force.
//  2. agent_role_claim with an unresolved agent_id returns a response
//     carrying trigger_reason=no-agent-resolved.
//  3. effective_verbs on the mechanical response reflect the seeded
//     role's allowed_mcp_verbs.
func TestAgentsRolesMechanical_ProviderOverride_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping testcontainers test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()

	net, err := network.New(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = net.Remove(ctx) })

	surreal, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "surrealdb/surrealdb:v3.0.0",
			ExposedPorts: []string{"8000/tcp"},
			Cmd:          []string{"start", "--user", "root", "--pass", "root"},
			Networks:     []string{net.Name},
			NetworkAliases: map[string][]string{
				net.Name: {"surrealdb"},
			},
			WaitingFor: wait.ForListeningPort("8000/tcp").WithStartupTimeout(90 * time.Second),
		},
		Started: true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = surreal.Terminate(ctx) })

	docsHost := filepath.Join(repoRoot(t), "docs")
	baseURL, stop := startServerContainerWithOptions(t, ctx, startOptions{
		Network: net.Name,
		Env: map[string]string{
			"DB_DSN":              "ws://root:root@surrealdb:8000/rpc/satellites/satellites",
			"SATELLITES_API_KEYS": "key_mech",
			"DOCS_DIR":            "/app/docs",
		},
		Mounts: []mount.Mount{{
			Type:     mount.TypeBind,
			Source:   docsHost,
			Target:   "/app/docs",
			ReadOnly: true,
		}},
	})
	defer stop()

	mcpURL := baseURL + "/mcp"
	rpcInit(t, ctx, mcpURL, "key_mech")

	// Create a role doc explicitly to avoid the document_get project_id
	// default-project resolution edge case for system-scope seeds.
	role := callTool(t, ctx, mcpURL, "key_mech", "role_create", map[string]any{
		"scope":      "system",
		"name":       "role_mech_it",
		"structured": `{"allowed_mcp_verbs":["document_get","document_list"]}`,
	})
	roleID, _ := role["id"].(string)
	require.NotEmpty(t, roleID)

	// Step 1: provider_override="mechanical" — explicit-force.
	force := callTool(t, ctx, mcpURL, "key_mech", "agent_role_claim", map[string]any{
		"workspace_id":      "wksp_it",
		"role_id":           roleID,
		"grantee_kind":      "session",
		"grantee_id":        "sess_force",
		"provider_override": "mechanical",
	})
	assert.Equal(t, "mechanical", force["provider"])
	assert.Equal(t, "explicit-force", force["trigger_reason"])
	assert.Equal(t, "active", force["status"])
	verbs, _ := force["effective_verbs"].([]any)
	assert.NotEmpty(t, verbs, "mechanical response should surface effective_verbs from seeded role")

	// Step 2: agent_id pointing at a non-existent doc — no-agent-resolved.
	noAgent := callTool(t, ctx, mcpURL, "key_mech", "agent_role_claim", map[string]any{
		"workspace_id": "wksp_it",
		"role_id":      roleID,
		"agent_id":     "doc_does_not_exist",
		"grantee_kind": "session",
		"grantee_id":   "sess_noagent",
	})
	assert.Equal(t, "mechanical", noAgent["provider"])
	assert.Equal(t, "no-agent-resolved", noAgent["trigger_reason"])
}
