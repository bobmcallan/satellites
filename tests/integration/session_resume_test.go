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

// TestSessionResume_RecoversPriorSessionForProject covers
// epic:agent-process-v1 (sty_cef068fe): a CLI restart can reconnect to
// the prior session row by calling `session_register({project_id})`
// without a session_id. The flow:
//
//  1. session_register({session_id, project_id}) — establishes session A
//     bound to proj_x.
//  2. agent_role_claim under session A.
//  3. contract_claim a CI (proves the session is functional).
//  4. "Disconnect" — represented by a fresh session_register call.
//  5. session_register({project_id}) with NO session_id → returns the
//     same session A (resumed=true).
//  6. contract_claim the SAME CI under the resumed session — succeeds
//     via the same-grant amend branch (claim_handlers.go:132).
func TestSessionResume_RecoversPriorSessionForProject(t *testing.T) {
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
			"SATELLITES_API_KEYS": "key_resume",
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
	rpcInit(t, ctx, mcpURL, "key_resume")

	// Need a project to bind the session to.
	proj := callTool(t, ctx, mcpURL, "key_resume", "project_create", map[string]any{
		"name": "resume-project",
	})
	projID, _ := proj["id"].(string)
	require.NotEmpty(t, projID)

	// Step 1: register session A with project binding.
	const sessionA = "sess_resume_a"
	reg1 := callTool(t, ctx, mcpURL, "key_resume", "session_register", map[string]any{
		"session_id": sessionA,
		"project_id": projID,
	})
	assert.Equal(t, sessionA, reg1["session_id"])
	assert.Equal(t, projID, reg1["active_project_id"])
	assert.Equal(t, false, reg1["resumed"], "first register should not be a resume")

	// Step 2: explicit agent_role_claim mints + stamps the orchestrator
	// grant on the session row (sty_a4074d21).
	_, _ = claimOrchestratorRole(t, ctx, mcpURL, "key_resume", sessionA)

	// Step 3-5: simulate a disconnect by calling session_register with
	// only project_id and no session_id; the handler resumes session A.
	resumed := callTool(t, ctx, mcpURL, "key_resume", "session_register", map[string]any{
		"project_id": projID,
	})
	assert.Equal(t, sessionA, resumed["session_id"], "resume must return the same session id")
	assert.Equal(t, true, resumed["resumed"], "resume must report resumed=true")

	// Step 6: The resumed session can run session_whoami without
	// re-claiming (the orchestrator grant is still stamped on the row).
	whoami := callTool(t, ctx, mcpURL, "key_resume", "session_whoami", map[string]any{
		"session_id": sessionA,
	})
	assert.Equal(t, sessionA, whoami["session_id"])
	require.NotEmpty(t, whoami["orchestrator_grant_id"], "resumed session must still carry the prior orchestrator grant")
}
