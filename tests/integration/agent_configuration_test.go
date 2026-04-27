package integration

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/mount"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestAgentConfiguration_PrecedenceAndReverseFK exercises the agent
// default_configuration_id feature added in story_fb600b97. Walk:
//
//  1. Create project + 4 contracts + Configuration A and B + agent doc
//     whose default_configuration_id = A.
//  2. Story 1 (no configuration_id) + workflow_claim with agent_id →
//     CIs match A (agent default fires).
//  3. Story 2 (configuration_id = B) + workflow_claim with agent_id →
//     CIs match B (story wins over agent).
//  4. Story 3 (no configuration_id) + workflow_claim WITHOUT agent_id
//     → CIs match the project default workflow_spec.
//  5. document_delete A while agent refs it → rejected with the agent
//     id named.
//  6. Update agent to clear default_configuration_id; retry delete →
//     succeeds.
func TestAgentConfiguration_PrecedenceAndReverseFK(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping testcontainers test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()

	net, err := network.New(ctx)
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
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
	if err != nil {
		t.Fatalf("start surrealdb: %v", err)
	}
	t.Cleanup(func() { _ = surreal.Terminate(ctx) })

	docsHost := filepath.Join(repoRoot(t), "docs")
	baseURL, stop := startServerContainerWithOptions(t, ctx, startOptions{
		Network: net.Name,
		Env: map[string]string{
			"DB_DSN":              "ws://root:root@surrealdb:8000/rpc/satellites/satellites",
			"SATELLITES_API_KEYS": "key_agentcfg",
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
	rpcInit(t, ctx, mcpURL, "key_agentcfg")

	// 1. Project + contracts.
	proj := callTool(t, ctx, mcpURL, "key_agentcfg", "project_create", map[string]any{"name": "agent-cfg-proj"})
	projectID, _ := proj["id"].(string)
	contractIDs := map[string]string{}
	for _, name := range []string{"preplan", "plan", "develop", "story_close"} {
		c := callTool(t, ctx, mcpURL, "key_agentcfg", "document_create", map[string]any{
			"type":       "contract",
			"scope":      "project",
			"project_id": projectID,
			"name":       name,
			"body":       name + " contract",
		})
		id, _ := c["id"].(string)
		contractIDs[name] = id
	}

	// Configuration A: 4 ordered refs.
	cfgAPayload, _ := json.Marshal(map[string]any{
		"contract_refs": []string{
			contractIDs["preplan"],
			contractIDs["plan"],
			contractIDs["develop"],
			contractIDs["story_close"],
		},
		"skill_refs":     []string{},
		"principle_refs": []string{},
	})
	cfgA := callTool(t, ctx, mcpURL, "key_agentcfg", "document_create", map[string]any{
		"type":       "configuration",
		"scope":      "project",
		"project_id": projectID,
		"name":       "config-A",
		"structured": string(cfgAPayload),
	})
	cfgAID, _ := cfgA["id"].(string)

	// Configuration B: 5 refs (extra develop) — distinct shape.
	cfgBPayload, _ := json.Marshal(map[string]any{
		"contract_refs": []string{
			contractIDs["preplan"],
			contractIDs["plan"],
			contractIDs["develop"],
			contractIDs["develop"],
			contractIDs["story_close"],
		},
		"skill_refs":     []string{},
		"principle_refs": []string{},
	})
	cfgB := callTool(t, ctx, mcpURL, "key_agentcfg", "document_create", map[string]any{
		"type":       "configuration",
		"scope":      "project",
		"project_id": projectID,
		"name":       "config-B",
		"structured": string(cfgBPayload),
	})
	cfgBID, _ := cfgB["id"].(string)

	// Agent doc with default_configuration_id = A.
	agentPayload, _ := json.Marshal(map[string]any{
		"default_configuration_id": cfgAID,
	})
	agent := callTool(t, ctx, mcpURL, "key_agentcfg", "document_create", map[string]any{
		"type":       "agent",
		"scope":      "project",
		"project_id": projectID,
		"name":       "agent-1",
		"body":       "agent body",
		"structured": string(agentPayload),
	})
	agentID, _ := agent["id"].(string)

	// document_get on the agent surfaces the resolved name.
	agentGet := callTool(t, ctx, mcpURL, "key_agentcfg", "document_get", map[string]any{"id": agentID})
	if got, _ := agentGet["default_configuration_name"].(string); got != "config-A" {
		t.Errorf("agent get: default_configuration_name = %q, want %q", got, "config-A")
	}

	// 2. Story 1 (no cfg) + workflow_claim with agent_id → CIs match A (4).
	story1 := callTool(t, ctx, mcpURL, "key_agentcfg", "story_create", map[string]any{
		"project_id": projectID,
		"title":      "story-no-cfg-with-agent",
	})
	story1ID, _ := story1["id"].(string)
	claim1 := callTool(t, ctx, mcpURL, "key_agentcfg", "workflow_claim", map[string]any{
		"story_id": story1ID,
		"agent_id": agentID,
	})
	cis1, _ := claim1["contract_instances"].([]any)
	if len(cis1) != 4 {
		t.Fatalf("agent-default workflow_claim CI count = %d, want 4", len(cis1))
	}

	// 3. Story 2 (configuration_id = B) + workflow_claim with agent_id →
	// CIs match B (5; story wins).
	story2 := callTool(t, ctx, mcpURL, "key_agentcfg", "story_create", map[string]any{
		"project_id":       projectID,
		"title":            "story-with-cfgB",
		"configuration_id": cfgBID,
	})
	story2ID, _ := story2["id"].(string)
	claim2 := callTool(t, ctx, mcpURL, "key_agentcfg", "workflow_claim", map[string]any{
		"story_id": story2ID,
		"agent_id": agentID,
	})
	cis2, _ := claim2["contract_instances"].([]any)
	if len(cis2) != 5 {
		t.Fatalf("story-wins-over-agent workflow_claim CI count = %d, want 5", len(cis2))
	}

	// 4. Story 3 (no cfg) + workflow_claim WITHOUT agent_id → project default.
	story3 := callTool(t, ctx, mcpURL, "key_agentcfg", "story_create", map[string]any{
		"project_id": projectID,
		"title":      "story-default",
	})
	story3ID, _ := story3["id"].(string)
	claim3 := callTool(t, ctx, mcpURL, "key_agentcfg", "workflow_claim", map[string]any{
		"story_id": story3ID,
	})
	cis3, _ := claim3["contract_instances"].([]any)
	if len(cis3) == 0 {
		t.Errorf("project-default workflow_claim returned no CIs; expected default-spec expansion")
	}

	// 5. document_delete A while agent refs it → rejected, agent id named.
	delResp := callToolRaw(t, ctx, mcpURL, "key_agentcfg", "document_delete", map[string]any{"id": cfgAID})
	if !isToolError(delResp) {
		t.Errorf("delete should be blocked while agent refs config; got %+v", delResp)
	}
	delText := toolErrorText(delResp)
	if !strings.Contains(delText, agentID) {
		t.Errorf("delete error must name referencing agent %q; got %q", agentID, delText)
	}

	// 6. Update agent to clear default_configuration_id; retry delete → succeeds.
	clearedAgentPayload, _ := json.Marshal(map[string]any{})
	_ = callTool(t, ctx, mcpURL, "key_agentcfg", "document_update", map[string]any{
		"id":         agentID,
		"structured": string(clearedAgentPayload),
	})
	finalDel := callTool(t, ctx, mcpURL, "key_agentcfg", "document_delete", map[string]any{"id": cfgAID})
	if got, _ := finalDel["deleted"].(bool); !got {
		t.Errorf("after agent cleared: delete should succeed; got %+v", finalDel)
	}
}
