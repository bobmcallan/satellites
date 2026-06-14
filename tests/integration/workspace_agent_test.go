//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/agent"
	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// fakeAgentModel is a deterministic AgentModel: it calls one read tool, then
// finalizes once it has seen the tool result. It proves the harness dispatches a
// real tool against the DB and feeds the result back — no live API.
type fakeAgentModel struct {
	calls  int
	sawDoc bool
}

func (f *fakeAgentModel) Step(_ context.Context, _ string, history []agent.Turn, _ []agent.Tool) (agent.AgentStep, error) {
	f.calls++
	if f.calls == 1 {
		return agent.AgentStep{ToolCalls: []agent.ToolCall{
			{Name: "document_get", Args: json.RawMessage(`{"name":"note"}`)},
		}}, nil
	}
	for _, turn := range history {
		if turn.Role == "tool" && strings.Contains(turn.ToolResult, "seeded body for the agent") {
			f.sawDoc = true
		}
	}
	return agent.AgentStep{Final: "AGENT SUMMARY"}, nil
}

// TestWorkspaceTaskRun_AgentMode drives workspace_task_run in agent mode with a
// fake model end-to-end: the harness dispatches document_get against the real DB
// through the allowlisted, workspace-scoped tool surface, feeds the result back,
// and stores the agent's final text as the output document. Deterministic — no
// live Gemini.
func TestWorkspaceTaskRun_AgentMode(t *testing.T) {
	env := testbootstrap.SetUp(t)
	docStore := document.New(env.DB)
	authStore := auth.New(env.DB)
	wsStore := workspace.New(env.DB)
	verb.SetDocumentStore(docStore)
	verb.SetAuthStore(authStore)
	verb.SetWorkspaceStore(wsStore)
	fake := &fakeAgentModel{}
	verb.SetAgentModel(fake)
	t.Cleanup(func() {
		verb.SetDocumentStore(nil)
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
		verb.SetAgentModel(nil)
	})

	if _, err := env.DB.Exec(`TRUNCATE api_keys, users, workspace_members RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if err := authStore.DevSeed(context.Background()); err != nil {
		t.Fatalf("dev seed: %v", err)
	}
	admin, err := authStore.GetUserByEmail(context.Background(), auth.DevAdminEmail)
	if err != nil {
		t.Fatalf("admin: %v", err)
	}
	ctx := authWithUser(context.Background(), admin)

	ws, err := wsStore.Create(context.Background(), admin.ID, "agent-ws", time.Now())
	if err != nil {
		t.Fatalf("create ws: %v", err)
	}
	if err := wsStore.AddMember(context.Background(), ws.ID, admin.ID, workspace.RoleAdmin, admin.ID, time.Now()); err != nil {
		t.Fatalf("add member: %v", err)
	}

	now := time.Now().UTC()
	// A corpus document the agent will fetch via the document_get tool.
	if _, _, err := docStore.Upsert(context.Background(), document.UpsertInput{
		Key:  document.Key{Scope: document.ScopeWorkspace, WorkspaceID: ws.ID, Name: "note"},
		Type: document.TypeDocument, Body: "seeded body for the agent", CreatedBy: admin.ID,
	}, now); err != nil {
		t.Fatalf("seed note: %v", err)
	}
	// The kind:task skill declares its tools (config) and its Spec; the agent
	// envelope is derived from these, not from Go literals.
	skillBody := "---\nname: summarise\nkind: task\ntools: [document_get]\n---\n## Spec\nSummarise the workspace corpus.\n"
	if _, _, err := docStore.Upsert(context.Background(), document.UpsertInput{
		Key:  document.Key{Scope: document.ScopeWorkspace, WorkspaceID: ws.ID, Name: "summarise"},
		Type: document.TypeSkill, Body: skillBody, CreatedBy: admin.ID,
	}, now); err != nil {
		t.Fatalf("seed skill: %v", err)
	}
	// The agent operating prompt is a system-scope document (config), not code —
	// seeded via the system-seed path (system scope is read-only to Upsert), the
	// same path the server boot uses for config/documents/agent-operating-prompt.md.
	sysSeed := document.NewSystemSeedStore(env.DB)
	if _, err := document.ReconcileSystemSeedTyped(context.Background(), sysSeed, docStore,
		document.TypeDocument, "agent-operating-prompt",
		"You are a workspace agent. Use the read-only tools, ground in results, produce the deliverable.",
		nil, "", "system:test", now); err != nil {
		t.Fatalf("seed agent prompt: %v", err)
	}

	raw, err := verb.Dispatch(ctx, "workspace_task_run", json.RawMessage(
		`{"workspace_id":"`+ws.ID+`","task_skill":{"scope":"workspace","name":"summarise"},"output_name":"corpus-summary","executor":"agent"}`))
	if err != nil {
		t.Fatalf("workspace_task_run agent: %v", err)
	}
	var resp verb.WorkspaceTaskRunResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Ran || resp.Result != "AGENT SUMMARY" {
		t.Fatalf("run resp = %+v", resp)
	}
	if !fake.sawDoc {
		t.Fatal("agent did not receive the document_get tool result from the real DB")
	}
	if fake.calls < 2 {
		t.Fatalf("expected >=2 model steps (tool then final), got %d", fake.calls)
	}

	// The agent's final text is stored as the output workspace document.
	gotRaw, err := verb.Dispatch(ctx, "document_get", json.RawMessage(
		`{"scope":"workspace","workspace_id":"`+ws.ID+`","name":"corpus-summary"}`))
	if err != nil {
		t.Fatalf("get result: %v", err)
	}
	var got verb.DocumentGetResponse
	if err := json.Unmarshal(gotRaw, &got); err != nil {
		t.Fatal(err)
	}
	if got.RawBody != "AGENT SUMMARY" {
		t.Fatalf("output doc raw_body = %q, want %q", got.RawBody, "AGENT SUMMARY")
	}
}
