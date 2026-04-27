package integration

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/moby/moby/api/types/mount"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestPlanAmend_AppendsCIWithACScope drives the dynamic plan-tree
// substrate (story_d5d88a64) end-to-end against a real testcontainers
// satellites server:
//
//  1. Boot Surreal + satellites server.
//  2. Create a project + the default contract documents.
//  3. Create a story with multi-AC acceptance criteria.
//  4. workflow_claim → 4 initial CIs (preplan/plan/develop/story_close).
//  5. plan_amend appends a develop CI scoped to AC=[2] under the original.
//  6. Assert: new CI carries ac_scope=[2] + parent_invocation_id =
//     original-develop-id; kind:plan-amend ledger row visible via
//     ledger_list with the expected Structured payload.
func TestPlanAmend_AppendsCIWithACScope(t *testing.T) {
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
			"SATELLITES_API_KEYS": "key_planamend",
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
	rpcInit(t, ctx, mcpURL, "key_planamend")

	project := callTool(t, ctx, mcpURL, "key_planamend", "project_create", map[string]any{
		"name": "plan-amend-project",
	})
	projectID, _ := project["id"].(string)

	// Seed contract docs at project scope so workflow_claim resolves.
	for _, name := range []string{"preplan", "plan", "develop", "story_close"} {
		_ = callTool(t, ctx, mcpURL, "key_planamend", "document_create", map[string]any{
			"type":       "contract",
			"scope":      "project",
			"project_id": projectID,
			"name":       name,
			"body":       name + " contract",
		})
	}

	storyResp := callTool(t, ctx, mcpURL, "key_planamend", "story_create", map[string]any{
		"project_id":          projectID,
		"title":               "story for plan-amend",
		"acceptance_criteria": "1. AC one.\n2. AC two needs rework.\n3. AC three.",
	})
	storyID, _ := storyResp["id"].(string)

	claim := callTool(t, ctx, mcpURL, "key_planamend", "workflow_claim", map[string]any{
		"story_id":           storyID,
		"proposed_contracts": []string{"preplan", "plan", "develop", "story_close"},
		"claim_markdown":     "initial workflow",
	})
	cisRaw, _ := claim["contract_instances"].([]any)
	if len(cisRaw) != 4 {
		t.Fatalf("workflow_claim CI count = %d, want 4", len(cisRaw))
	}
	var origDevelopID string
	for _, raw := range cisRaw {
		ci, _ := raw.(map[string]any)
		if name, _ := ci["contract_name"].(string); name == "develop" {
			origDevelopID, _ = ci["id"].(string)
			break
		}
	}
	if origDevelopID == "" {
		t.Fatalf("original develop CI id missing from workflow_claim response")
	}

	addsJSON, _ := json.Marshal([]map[string]any{
		{
			"contract_name":        "develop",
			"ac_scope":             []int{2},
			"parent_invocation_id": origDevelopID,
		},
	})
	amend := callTool(t, ctx, mcpURL, "key_planamend", "plan_amend", map[string]any{
		"story_id":        storyID,
		"add_invocations": string(addsJSON),
		"reason":          "rework AC 2 after develop close",
	})
	amendCIs, _ := amend["contract_instances"].([]any)
	if len(amendCIs) != 1 {
		t.Fatalf("plan_amend created CI count = %d, want 1", len(amendCIs))
	}
	added, _ := amendCIs[0].(map[string]any)
	if name, _ := added["contract_name"].(string); name != "develop" {
		t.Errorf("amend CI contract_name = %q, want develop", name)
	}
	if pid, _ := added["parent_invocation_id"].(string); pid != origDevelopID {
		t.Errorf("amend CI parent_invocation_id = %q, want %q", pid, origDevelopID)
	}
	scopeRaw, _ := added["ac_scope"].([]any)
	if len(scopeRaw) != 1 {
		t.Fatalf("amend CI ac_scope length = %d, want 1", len(scopeRaw))
	}
	if v, _ := scopeRaw[0].(float64); int(v) != 2 {
		t.Errorf("amend CI ac_scope[0] = %v, want 2", v)
	}
	if id, _ := amend["plan_amend_ledger_id"].(string); id == "" {
		t.Error("plan_amend_ledger_id empty in response")
	}

	// kind:plan-amend ledger row is queryable + carries the expected
	// structured payload.
	ledgerList := callToolArray(t, ctx, mcpURL, "key_planamend", "ledger_list", map[string]any{
		"story_id": storyID,
		"type":     "plan-amend",
	})
	if len(ledgerList) == 0 {
		t.Fatalf("ledger_list type=plan-amend returned no rows")
	}
	row, _ := ledgerList[0].(map[string]any)
	if got, _ := row["type"].(string); got != "plan-amend" {
		t.Errorf("ledger row type = %q, want plan-amend", got)
	}
	if got, _ := row["content"].(string); got != "rework AC 2 after develop close" {
		t.Errorf("ledger content = %q, want the amend reason verbatim", got)
	}
	structured, _ := row["structured"].(map[string]any)
	if structured == nil {
		t.Fatalf("ledger row missing structured payload")
	}
	if structured["reason"] != "rework AC 2 after develop close" {
		t.Errorf("structured.reason mismatch: %+v", structured["reason"])
	}
	if _, ok := structured["added_cis"].([]any); !ok {
		t.Errorf("structured.added_cis missing or wrong shape: %+v", structured["added_cis"])
	}

	// story_get returns CIs in tree-walk order — the amended develop CI
	// must come immediately after the original develop CI.
	storyGet := callTool(t, ctx, mcpURL, "key_planamend", "story_get", map[string]any{
		"id": storyID,
	})
	cisOut, _ := storyGet["contract_instances"].([]any)
	if len(cisOut) != 5 {
		t.Fatalf("story_get CI count = %d, want 5", len(cisOut))
	}
	var origIdx, amendIdx = -1, -1
	for i, raw := range cisOut {
		ci, _ := raw.(map[string]any)
		id, _ := ci["id"].(string)
		switch id {
		case origDevelopID:
			origIdx = i
		case added["id"].(string):
			amendIdx = i
		}
	}
	if origIdx < 0 || amendIdx < 0 {
		t.Fatalf("story_get response missing original (idx=%d) or amended (idx=%d) develop CI", origIdx, amendIdx)
	}
	if amendIdx != origIdx+1 {
		t.Errorf("tree-walk order: amended develop at idx %d, expected immediately after original at idx %d", amendIdx, origIdx)
	}
}
