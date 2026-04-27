package integration

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/mount"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestAgentsRolesGrantReleaseReclaim_EndToEnd covers AC 6 of
// story_4608a82c by exercising the full five-step release/re-claim
// flow through the MCP surface against a live SurrealDB:
//
//  1. Session A registers → mints orchestrator grant A.
//  2. Session A claims a CI — succeeds (claimed_via_grant_id = A).
//  3. agent_role_release on grant A.
//  4. Session A attempts to claim a different CI → rejected with
//     grant_required (the stamped grant is no longer active).
//  5. Session A re-registers → mints fresh grant B.
//  6. Session A claims the same CI — succeeds (claimed_via_grant_id = B).
//
// This is the proof that the grant-based process-order gate handles the
// full lifecycle: the release path invalidates existing stamps; the
// re-registration path re-qualifies the session; the handler can tell
// the two apart.
func TestAgentsRolesGrantReleaseReclaim_EndToEnd(t *testing.T) {
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
			"SATELLITES_API_KEYS": "key_grc",
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
	rpcInit(t, ctx, mcpURL, "key_grc")

	// Step 1: session A registers → grant A minted and stamped.
	const sessionA = "sess_grc_a"
	reg1 := callTool(t, ctx, mcpURL, "key_grc", "session_register", map[string]any{
		"session_id": sessionA,
	})
	grantA, _ := reg1["orchestrator_grant_id"].(string)
	require.NotEmpty(t, grantA, "SessionStart should mint a grant")

	// Resolve the seed role_orchestrator's document id so every seeded
	// contract's `required_role` matches the grant's RoleID (both
	// compare as document ids, not names).
	roles := callToolArray(t, ctx, mcpURL, "key_grc", "document_list", map[string]any{
		"type":  "role",
		"scope": "system",
	})
	roleID := ""
	for _, raw := range roles {
		m, _ := raw.(map[string]any)
		if name, _ := m["name"].(string); name == "role_orchestrator" {
			if id, _ := m["id"].(string); id != "" {
				roleID = id
				break
			}
		}
	}
	require.NotEmpty(t, roleID, "role_orchestrator seed must be resolvable")
	requiredRoleJSON := `{"category":"lifecycle","required_for_close":true,"validation_mode":"agent","required_role":"` + roleID + `"}`

	// Seed the 4 lifecycle contract docs the workflow_claim will
	// instantiate against. Each carries required_role so the claim gate
	// exercises the full grant-based path.
	for _, name := range []string{"preplan", "plan", "develop", "story_close"} {
		_ = callTool(t, ctx, mcpURL, "key_grc", "contract_create", map[string]any{
			"scope":      "system",
			"name":       name,
			"body":       "contract " + name + " for release-reclaim test",
			"structured": requiredRoleJSON,
		})
	}

	// Create project + story to get a claimable CI.
	proj := callTool(t, ctx, mcpURL, "key_grc", "project_create", map[string]any{
		"name": "grc-release-reclaim",
	})
	projID, _ := proj["id"].(string)
	require.NotEmpty(t, projID)

	story := callTool(t, ctx, mcpURL, "key_grc", "story_create", map[string]any{
		"project_id":          projID,
		"title":               "grc-test",
		"description":         "end-to-end release/re-claim",
		"acceptance_criteria": "covered by test assertions",
		"priority":            "high",
		"category":            "feature",
	})
	storyID, _ := story["id"].(string)
	require.NotEmpty(t, storyID)

	// Instantiate the workflow — the 4-slot shape the system contracts seed.
	wf := callTool(t, ctx, mcpURL, "key_grc", "workflow_claim", map[string]any{
		"story_id":           storyID,
		"proposed_contracts": []any{"preplan", "plan", "develop", "story_close"},
		"claim_markdown":     "standard shape",
	})
	cisRaw, _ := wf["contract_instances"].([]any)
	require.Len(t, cisRaw, 4, "workflow_claim should instantiate 4 CIs")
	ciIDs := make([]string, 0, 4)
	for _, raw := range cisRaw {
		m, _ := raw.(map[string]any)
		if id, _ := m["id"].(string); id != "" {
			ciIDs = append(ciIDs, id)
		}
	}
	require.Len(t, ciIDs, 4)

	// Step 2: session A claims the preplan CI — succeeds under grant A.
	claim1 := callTool(t, ctx, mcpURL, "key_grc", "contract_claim", map[string]any{
		"contract_instance_id": ciIDs[0],
		"session_id":           sessionA,
		"permissions_claim":    []any{"Read:**"},
	})
	assert.Equal(t, "claimed", claim1["status"])

	// Close the preplan so the predecessor gate lets us claim the next
	// CI later.
	_ = callTool(t, ctx, mcpURL, "key_grc", "contract_close", map[string]any{
		"contract_instance_id": ciIDs[0],
		"close_markdown":       "preplan done",
		"evidence_markdown":    "baseline evidence",
		"proposed_workflow":    []any{"preplan", "plan", "develop", "story_close"},
	})

	// Step 3: release session A's grant.
	rel := callTool(t, ctx, mcpURL, "key_grc", "agent_role_release", map[string]any{
		"grant_id": grantA,
		"reason":   "test: end of phase 1",
	})
	assert.Equal(t, "released", rel["status"])

	// Step 4: attempt to claim the plan CI under the same session — rejected.
	// The session's OrchestratorGrantID still points at grant A, but
	// grant A is no longer active, so resolveRequiredRoleGrant returns
	// grant_required.
	rejectResp := callToolRaw(t, ctx, mcpURL, "key_grc", "contract_claim", map[string]any{
		"contract_instance_id": ciIDs[1],
		"session_id":           sessionA,
	})
	require.True(t, isToolError(rejectResp), "claim should fail after grant release")
	rejectText := extractToolText(t, rejectResp)
	assert.True(t,
		strings.Contains(rejectText, "grant_required"),
		"claim after release should report grant_required; got %s", rejectText,
	)

	// Step 5: re-register session A → mints fresh grant B.
	reg2 := callTool(t, ctx, mcpURL, "key_grc", "session_register", map[string]any{
		"session_id": sessionA,
	})
	grantB, _ := reg2["orchestrator_grant_id"].(string)
	require.NotEmpty(t, grantB, "re-register should mint a fresh grant")
	assert.NotEqual(t, grantA, grantB, "fresh registration must mint a distinct grant")

	// Step 6: session A claims the plan CI — succeeds under grant B.
	claim2 := callTool(t, ctx, mcpURL, "key_grc", "contract_claim", map[string]any{
		"contract_instance_id": ciIDs[1],
		"session_id":           sessionA,
		"permissions_claim":    []any{"Read:**"},
	})
	assert.Equal(t, "claimed", claim2["status"])
}
