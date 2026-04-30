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

// TestAgentsRoles_Wrappers_Registered_AndCreateHappyPaths verifies the
// 12 new MCP wrapper verbs (agent_* + role_*) are registered after the
// 6.2 schema extension lands, and exercises an agent_create + role_create
// happy path against the container stack.
//
// AC 6 per story_045a613f: "Integration test via tests/common/containers
// boots a local satellites server, creates one agent document + one role
// document + one role_grant row, reads them back — green before push."
//
// The role_grant row half of the AC is exercised at the store layer in
// internal/rolegrant/store_test.go (MemoryStore + SurrealStore tests
// covering Create, Release, List, concurrent-grant, workspace isolation,
// FK integrity). 6.2 does not ship MCP verbs for the grant lifecycle —
// those arrive with 6.3 (story_1efbfc48) — so the MCP-facing half of
// AC 6 stops at the agent + role wrappers.
func TestAgentsRoles_Wrappers_Registered_AndCreateHappyPaths(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping testcontainers test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()

	net, err := network.New(ctx)
	require.NoError(t, err, "create network")
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
	require.NoError(t, err, "start surrealdb")
	t.Cleanup(func() { _ = surreal.Terminate(ctx) })

	docsHost := filepath.Join(repoRoot(t), "docs")
	baseURL, stop := startServerContainerWithOptions(t, ctx, startOptions{
		Network: net.Name,
		Env: map[string]string{
			"SATELLITES_DB_DSN":   "ws://root:root@surrealdb:8000/rpc/satellites/satellites",
			"SATELLITES_API_KEYS": "key_ar",
			"SATELLITES_DOCS_DIR": "/app/docs",
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
	rpcInit(t, ctx, mcpURL, "key_ar")

	// Assert the 12 new wrapper verbs (agent_* + role_* × 6 ops each)
	// show up in tools/list.
	listResp := rpcCall(t, ctx, mcpURL, "key_ar", map[string]any{
		"jsonrpc": "2.0", "id": 10, "method": "tools/list",
	})
	want := map[string]bool{}
	for _, kind := range []string{"agent", "role"} {
		for _, op := range []string{"_create", "_get", "_list", "_update", "_delete", "_search"} {
			want[kind+op] = false
		}
	}
	if result, _ := listResp["result"].(map[string]any); result != nil {
		if tools, _ := result["tools"].([]any); tools != nil {
			for _, raw := range tools {
				if tool, ok := raw.(map[string]any); ok {
					if name, _ := tool["name"].(string); name != "" {
						if _, tracked := want[name]; tracked {
							want[name] = true
						}
					}
				}
			}
		}
	}
	for k, ok := range want {
		assert.True(t, ok, "tools/list missing wrapper %q", k)
	}

	// agent_create happy path with an optional contract_binding omitted.
	agent := callTool(t, ctx, mcpURL, "key_ar", "agent_create", map[string]any{
		"scope":      "system",
		"name":       "agent_it_claude",
		"body":       "provider_chain=[claude], tier=opus",
		"structured": `{"provider_chain":[{"provider":"claude","model":"opus-4"}],"tier":"opus","permitted_roles":["role_orchestrator"],"tool_ceiling":["*"]}`,
	})
	assert.Equal(t, "agent", agent["type"], "agent_create should pin type=agent")
	agentID, _ := agent["id"].(string)
	require.NotEmpty(t, agentID, "agent_create should return id")

	// role_create happy path at scope=workspace.
	role := callTool(t, ctx, mcpURL, "key_ar", "role_create", map[string]any{
		"scope":      "system",
		"name":       "role_it_custom",
		"body":       "RBAC bundle for integration test",
		"structured": `{"allowed_mcp_verbs":["document_get","document_list"],"required_hooks":["SessionStart"],"claim_requirements":[],"default_context_policy":"fresh-per-claim"}`,
	})
	assert.Equal(t, "role", role["type"], "role_create should pin type=role")
	roleID, _ := role["id"].(string)
	require.NotEmpty(t, roleID, "role_create should return id")

	// Read back via agent_get / role_get by id.
	agentGot := callTool(t, ctx, mcpURL, "key_ar", "agent_get", map[string]any{"id": agentID})
	assert.Equal(t, "agent", agentGot["type"])
	assert.Equal(t, "agent_it_claude", agentGot["name"])

	roleGot := callTool(t, ctx, mcpURL, "key_ar", "role_get", map[string]any{"id": roleID})
	assert.Equal(t, "role", roleGot["type"])
	assert.Equal(t, "role_it_custom", roleGot["name"])

	// agent_list pins type=agent even when caller tries to escape.
	agentRows := callToolArray(t, ctx, mcpURL, "key_ar", "agent_list", map[string]any{
		"type": "role",
	})
	for _, row := range agentRows {
		m, _ := row.(map[string]any)
		assert.Equal(t, "agent", m["type"], "agent_list returned non-agent row: %+v", m)
	}

	// role_list pins type=role.
	roleRows := callToolArray(t, ctx, mcpURL, "key_ar", "role_list", map[string]any{
		"type": "agent",
	})
	for _, row := range roleRows {
		m, _ := row.(map[string]any)
		assert.Equal(t, "role", m["type"], "role_list returned non-role row: %+v", m)
	}
}
