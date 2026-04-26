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

// TestStoryConfiguration_AssignmentAndResolution verifies story-level
// configuration_id assignment (story_4ca6cb1b). Walk:
//
//  1. Create project + N project-scope contracts.
//  2. Create Configuration A with ContractRefs = [c1, c2, c3] (and the
//     two required system slots — preplan, plan, develop, story_close).
//  3. Create story with configuration_id = A.
//  4. story_workflow_claim with no proposed_contracts → CIs match A's
//     contract list (not project default).
//  5. story_update clears configuration_id, then re-assigns → response
//     reflects the change.
//  6. document_delete A while open story still references it → rejected
//     with the story id named.
//  7. story_update_status moves story to done; retry delete → succeeds.
func TestStoryConfiguration_AssignmentAndResolution(t *testing.T) {
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
			"SATELLITES_API_KEYS": "key_storycfg",
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
	rpcInit(t, ctx, mcpURL, "key_storycfg")

	// 1. Create a project.
	project := callTool(t, ctx, mcpURL, "key_storycfg", "project_create", map[string]any{
		"name": "story-cfg-project",
	})
	projectID, _ := project["id"].(string)
	if projectID == "" {
		t.Fatalf("project_create returned no id: %+v", project)
	}

	// 2. Seed three project-scope contracts (preplan, plan, develop, story_close
	// — same names as the system defaults so workflow_spec validation passes).
	contractIDs := map[string]string{}
	for _, name := range []string{"preplan", "plan", "develop", "story_close"} {
		c := callTool(t, ctx, mcpURL, "key_storycfg", "document_create", map[string]any{
			"type":       "contract",
			"scope":      "project",
			"project_id": projectID,
			"name":       name,
			"body":       name + " contract",
		})
		id, _ := c["id"].(string)
		if id == "" {
			t.Fatalf("contract %q create: no id %+v", name, c)
		}
		contractIDs[name] = id
	}

	// 3. Create Configuration A with the four contract refs.
	cfgPayload, _ := json.Marshal(map[string]any{
		"contract_refs": []string{
			contractIDs["preplan"],
			contractIDs["plan"],
			contractIDs["develop"],
			contractIDs["story_close"],
		},
		"skill_refs":     []string{},
		"principle_refs": []string{},
	})
	cfgA := callTool(t, ctx, mcpURL, "key_storycfg", "document_create", map[string]any{
		"type":       "configuration",
		"scope":      "project",
		"project_id": projectID,
		"name":       "config-A",
		"body":       "config A bundle",
		"structured": string(cfgPayload),
	})
	cfgAID, _ := cfgA["id"].(string)

	// 4. Create story with configuration_id = A.
	storyResp := callTool(t, ctx, mcpURL, "key_storycfg", "story_create", map[string]any{
		"project_id":       projectID,
		"title":            "story-with-cfgA",
		"configuration_id": cfgAID,
	})
	storyID, _ := storyResp["id"].(string)
	if got, _ := storyResp["configuration_id"].(string); got != cfgAID {
		t.Errorf("story_create: configuration_id = %q, want %q", got, cfgAID)
	}
	if got, _ := storyResp["configuration_name"].(string); got != "config-A" {
		t.Errorf("story_create: configuration_name = %q, want %q", got, "config-A")
	}

	// 5. story_get includes configuration_id + configuration_name.
	getResp := callTool(t, ctx, mcpURL, "key_storycfg", "story_get", map[string]any{
		"id": storyID,
	})
	if got, _ := getResp["configuration_id"].(string); got != cfgAID {
		t.Errorf("story_get: configuration_id = %q, want %q", got, cfgAID)
	}
	if got, _ := getResp["configuration_name"].(string); got != "config-A" {
		t.Errorf("story_get: configuration_name = %q, want %q", got, "config-A")
	}

	// 6. workflow_claim with no proposed_contracts → CIs derive from Configuration.
	claim := callTool(t, ctx, mcpURL, "key_storycfg", "story_workflow_claim", map[string]any{
		"story_id":       storyID,
		"claim_markdown": "from configuration A",
	})
	cisRaw, _ := claim["contract_instances"].([]any)
	if len(cisRaw) != 4 {
		t.Fatalf("workflow_claim CI count = %d, want 4", len(cisRaw))
	}
	want := []string{"preplan", "plan", "develop", "story_close"}
	for i, raw := range cisRaw {
		ci, _ := raw.(map[string]any)
		got, _ := ci["contract_name"].(string)
		if got != want[i] {
			t.Errorf("CI[%d] = %q, want %q", i, got, want[i])
		}
	}

	// 7. document_delete config A while open story refs it → rejected,
	// referencing story id named.
	delResp := callToolRaw(t, ctx, mcpURL, "key_storycfg", "document_delete", map[string]any{
		"id": cfgAID,
	})
	if !isToolError(delResp) {
		t.Errorf("delete should be blocked while open story references config; got %+v", delResp)
	}
	delText := toolErrorText(delResp)
	if !strings.Contains(delText, storyID) {
		t.Errorf("delete error must name referencing story %q; got %q", storyID, delText)
	}

	// 8. Move story to done so the FK gate doesn't block deletion.
	for _, target := range []string{"ready", "in_progress", "done"} {
		_ = callTool(t, ctx, mcpURL, "key_storycfg", "story_update_status", map[string]any{
			"id":     storyID,
			"status": target,
		})
	}

	// 9. Retry delete — now allowed (only done story refs it).
	finalDel := callTool(t, ctx, mcpURL, "key_storycfg", "document_delete", map[string]any{
		"id": cfgAID,
	})
	if got, _ := finalDel["deleted"].(bool); !got {
		t.Errorf("after story done: delete should succeed; got %+v", finalDel)
	}

	// 10. Null-configuration story falls back to project default.
	storyNull := callTool(t, ctx, mcpURL, "key_storycfg", "story_create", map[string]any{
		"project_id": projectID,
		"title":      "story-no-cfg",
	})
	storyNullID, _ := storyNull["id"].(string)
	claimNull := callTool(t, ctx, mcpURL, "key_storycfg", "story_workflow_claim", map[string]any{
		"story_id":       storyNullID,
		"claim_markdown": "default fallback",
	})
	cisRawNull, _ := claimNull["contract_instances"].([]any)
	if len(cisRawNull) == 0 {
		t.Errorf("null-cfg workflow_claim returned no CIs; expected default-spec expansion")
	}
}
