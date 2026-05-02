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

// TestAgentsRolesOrchestrator_ExplicitClaim_IssuesGrant boots the full
// container stack, seeds role_orchestrator + agent_claude_orchestrator
// at server bootstrap (inline in cmd/satellites/main.go), then exercises
// the post-sty_a4074d21 explicit-claim flow:
//
//  1. session_register → no auto-grant; orchestrator_grant_id empty.
//  2. agent_role_claim with grantee_kind=session → grant minted, stamped
//     on the session row.
//  3. session_whoami returns the grant id + effective_verbs.
//  4. Two distinct sessions each agent_role_claim independently and
//     receive distinct grants coexisting active.
func TestAgentsRolesOrchestrator_ExplicitClaim_IssuesGrant(t *testing.T) {
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
			"SATELLITES_DB_DSN":   "ws://root:root@surrealdb:8000/rpc/satellites/satellites",
			"SATELLITES_API_KEYS": "key_oc",
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
	rpcInit(t, ctx, mcpURL, "key_oc")

	// Step 1: register session α — no auto-grant.
	reg1 := callTool(t, ctx, mcpURL, "key_oc", "session_register", map[string]any{
		"session_id": "sess_alpha",
	})
	if g, _ := reg1["orchestrator_grant_id"].(string); g != "" {
		t.Fatalf("session_register must not mint an orchestrator grant; got %q", g)
	}

	// Step 2: explicit agent_role_claim mints + stamps the grant.
	grant1, _ := claimOrchestratorRole(t, ctx, mcpURL, "key_oc", "sess_alpha")

	// Step 3: whoami returns grant metadata + effective_verbs.
	whoami1 := callTool(t, ctx, mcpURL, "key_oc", "session_whoami", map[string]any{
		"session_id": "sess_alpha",
	})
	assert.Equal(t, grant1, whoami1["orchestrator_grant_id"])
	verbs, _ := whoami1["effective_verbs"].([]any)
	assert.NotEmpty(t, verbs, "session_whoami should surface effective_verbs when grant is live")

	// Step 4: register + claim session β; confirm distinct grant + both coexist.
	_ = callTool(t, ctx, mcpURL, "key_oc", "session_register", map[string]any{
		"session_id": "sess_beta",
	})
	grant2, _ := claimOrchestratorRole(t, ctx, mcpURL, "key_oc", "sess_beta")
	assert.NotEqual(t, grant1, grant2, "distinct sessions should receive distinct grants")

	// agent_role_list filtered by status=active should return both.
	active := callToolArray(t, ctx, mcpURL, "key_oc", "agent_role_list", map[string]any{
		"status": "active",
	})
	activeCount := 0
	for _, row := range active {
		m, _ := row.(map[string]any)
		if m["status"] == "active" {
			activeCount++
		}
	}
	assert.GreaterOrEqual(t, activeCount, 2, "both alpha + beta grants should be active")
}
