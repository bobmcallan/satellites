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

// TestDocumentConfiguration_CRUD exercises the type=configuration
// substrate added by story_d371f155 against a real SurrealDB + satellites
// server binary. The walk:
//
//  1. Create a project (configurations are project-scoped).
//  2. Create contracts, skills, and principles inside it.
//  3. document_create type=configuration with valid refs → success;
//     structured payload echoes refs.
//  4. document_list filtered by type=configuration → returns the new row.
//  5. document_get by id → round-trips.
//  6. document_update flipping a contract_ref to a non-existent id →
//     rejected with FK error naming the missing id.
//  7. document_update with a fresh valid ref set → succeeds, version
//     bumps via the body change path.
//  8. document_delete archive (default).
func TestDocumentConfiguration_CRUD(t *testing.T) {
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
			"SATELLITES_API_KEYS": "key_cfg",
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
	rpcInit(t, ctx, mcpURL, "key_cfg")

	// 1. Create a project (configurations require scope=project).
	project := callTool(t, ctx, mcpURL, "key_cfg", "project_create", map[string]any{
		"name": "cfg-project",
	})
	projectID, _ := project["id"].(string)
	if projectID == "" {
		t.Fatalf("project_create returned no id: %+v", project)
	}

	// 2. Seed refs — one contract, one skill (bound to the contract), one principle.
	contract := callTool(t, ctx, mcpURL, "key_cfg", "document_create", map[string]any{
		"type":       "contract",
		"scope":      "project",
		"project_id": projectID,
		"name":       "cfg-develop",
		"body":       "develop contract body",
	})
	contractID, _ := contract["id"].(string)
	if contractID == "" {
		t.Fatalf("contract create returned no id: %+v", contract)
	}

	skill := callTool(t, ctx, mcpURL, "key_cfg", "document_create", map[string]any{
		"type":             "skill",
		"scope":            "project",
		"project_id":       projectID,
		"name":             "cfg-skill",
		"contract_binding": contractID,
		"body":             "skill body",
	})
	skillID, _ := skill["id"].(string)

	principle := callTool(t, ctx, mcpURL, "key_cfg", "document_create", map[string]any{
		"type":       "principle",
		"scope":      "project",
		"project_id": projectID,
		"name":       "cfg-principle",
		"body":       "principle body",
	})
	principleID, _ := principle["id"].(string)

	// 3. document_create type=configuration with valid refs.
	cfgPayload, _ := json.Marshal(map[string]any{
		"contract_refs":  []string{contractID},
		"skill_refs":     []string{skillID},
		"principle_refs": []string{principleID},
	})
	cfg := callTool(t, ctx, mcpURL, "key_cfg", "document_create", map[string]any{
		"type":       "configuration",
		"scope":      "project",
		"project_id": projectID,
		"name":       "frontend-config",
		"body":       "Frontend bundle",
		"structured": string(cfgPayload),
	})
	cfgID, _ := cfg["id"].(string)
	if cfgID == "" {
		t.Fatalf("configuration create returned no id: %+v", cfg)
	}
	if got, _ := cfg["type"].(string); got != "configuration" {
		t.Errorf("created type = %q, want configuration", got)
	}

	// 4. document_list filtered by type=configuration returns the new row.
	cfgList := callToolArray(t, ctx, mcpURL, "key_cfg", "document_list", map[string]any{
		"type":       "configuration",
		"project_id": projectID,
	})
	if len(cfgList) != 1 {
		t.Errorf("document_list(type=configuration) returned %d rows, want 1", len(cfgList))
	}

	// 5. document_get by id round-trips.
	got := callTool(t, ctx, mcpURL, "key_cfg", "document_get", map[string]any{"id": cfgID})
	if got["id"] != cfgID || got["name"] != "frontend-config" {
		t.Errorf("document_get(id) = %+v", got)
	}

	// 6. document_update flipping a contract_ref to a non-existent id is rejected.
	badPayload, _ := json.Marshal(map[string]any{
		"contract_refs":  []string{contractID, "doc_does_not_exist"},
		"skill_refs":     []string{skillID},
		"principle_refs": []string{principleID},
	})
	bad := callToolRaw(t, ctx, mcpURL, "key_cfg", "document_update", map[string]any{
		"id":         cfgID,
		"structured": string(badPayload),
	})
	if !isToolError(bad) {
		t.Errorf("update with dangling contract_ref should isError; got %+v", bad)
	}
	// Surface the error message so a reviewer can see the FK reason inline.
	badText := toolErrorText(bad)
	if !strings.Contains(badText, "doc_does_not_exist") {
		t.Errorf("dangling-ref error must name the missing id; got %q", badText)
	}

	// 7. document_update with a fresh valid ref set succeeds.
	newPayload, _ := json.Marshal(map[string]any{
		"contract_refs":  []string{contractID},
		"skill_refs":     []string{skillID},
		"principle_refs": []string{},
	})
	updated := callTool(t, ctx, mcpURL, "key_cfg", "document_update", map[string]any{
		"id":         cfgID,
		"structured": string(newPayload),
	})
	if updated["id"] != cfgID {
		t.Errorf("update returned id = %v, want %q", updated["id"], cfgID)
	}

	// 8. document_delete archives by default.
	delResp := callTool(t, ctx, mcpURL, "key_cfg", "document_delete", map[string]any{"id": cfgID})
	if delResp["deleted"] != true {
		t.Errorf("delete deleted = %v, want true", delResp["deleted"])
	}
	gotArchived := callTool(t, ctx, mcpURL, "key_cfg", "document_get", map[string]any{"id": cfgID})
	if gotArchived["status"] != "archived" {
		t.Errorf("after archive status = %v, want archived", gotArchived["status"])
	}
}

// toolErrorText extracts the human-readable text from an MCP tool-call
// error response. Returns the raw response stringified when the shape is
// unexpected, so the test failure surfaces enough context to debug.
func toolErrorText(resp map[string]any) string {
	if resp == nil {
		return "<nil>"
	}
	result, _ := resp["result"].(map[string]any)
	if result == nil {
		body, _ := json.Marshal(resp)
		return string(body)
	}
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		body, _ := json.Marshal(result)
		return string(body)
	}
	first, _ := content[0].(map[string]any)
	if first == nil {
		body, _ := json.Marshal(result)
		return string(body)
	}
	text, _ := first["text"].(string)
	return text
}
