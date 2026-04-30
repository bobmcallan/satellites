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

// TestTaskMCP_EnqueueClaimClose_EndToEnd exercises the 9.2 MCP surface:
// task_enqueue → task_list → task_claim → task_close. The apikey
// identity is auto-admin in the system workspace per boot seeding, so
// memberships include the workspace that task_enqueue cascades into.
func TestTaskMCP_EnqueueClaimClose_EndToEnd(t *testing.T) {
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
			"SATELLITES_API_KEYS": "key_task",
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
	rpcInit(t, ctx, mcpURL, "key_task")

	// Enqueue a task (workspace_id omitted → defaults to caller's first
	// membership, which the apikey identity shares with the system
	// workspace per boot seeding).
	enq := callTool(t, ctx, mcpURL, "key_task", "task_enqueue", map[string]any{
		"origin":   "scheduled",
		"priority": "high",
		"payload":  `{"job":"nightly"}`,
	})
	taskID, _ := enq["task_id"].(string)
	require.NotEmpty(t, taskID)
	assert.Equal(t, "enqueued", enq["status"])
	assert.NotEmpty(t, enq["ledger_root_id"])

	// List filtered to status=enqueued; confirm the row.
	rows := callToolArray(t, ctx, mcpURL, "key_task", "task_list", map[string]any{
		"status": "enqueued",
	})
	found := false
	for _, row := range rows {
		m, _ := row.(map[string]any)
		if m["id"] == taskID {
			found = true
		}
	}
	assert.True(t, found, "task_list should surface the enqueued row")

	// Claim picks the task.
	claim := callTool(t, ctx, mcpURL, "key_task", "task_claim", map[string]any{
		"worker_id": "worker_it",
	})
	assert.Equal(t, taskID, claim["id"], "task_claim should return the enqueued task")
	assert.Equal(t, "claimed", claim["status"])

	// Close with outcome=success.
	closed := callTool(t, ctx, mcpURL, "key_task", "task_close", map[string]any{
		"id":      taskID,
		"outcome": "success",
	})
	assert.Equal(t, "closed", closed["status"])
	assert.Equal(t, "success", closed["outcome"])
}
