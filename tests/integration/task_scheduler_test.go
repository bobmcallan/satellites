//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestTaskScheduler_RunsOnCadence drives the real scheduler over Postgres with a
// fake agent model (no live Gemini): a trigger:"schedule" task regenerates its
// output document on a short interval, with a ledger row per run (sty_7bf60667
// AC4). Time-driven, non-UI. The agent-task doc is written directly with a
// sub-floor interval — the upsert floor (60s) guards operators, not this test.
func TestTaskScheduler_RunsOnCadence(t *testing.T) {
	env := testbootstrap.SetUp(t)
	docStore := document.New(env.DB)
	authStore := auth.New(env.DB)
	wsStore := workspace.New(env.DB)
	ledgerStore := ledger.New(env.DB)
	verb.SetDocumentStore(docStore)
	verb.SetAuthStore(authStore)
	verb.SetWorkspaceStore(wsStore)
	verb.SetAgentModel(&fakeAgentModel{})
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
	ws, err := wsStore.Create(context.Background(), admin.ID, "sched-ws", time.Now())
	if err != nil {
		t.Fatalf("create ws: %v", err)
	}
	if err := wsStore.AddMember(context.Background(), ws.ID, admin.ID, workspace.RoleAdmin, admin.ID, time.Now()); err != nil {
		t.Fatalf("add member: %v", err)
	}

	now := time.Now().UTC()
	// Corpus doc the agent fetches, the kind:task skill, and the operating prompt.
	if _, _, err := docStore.Upsert(context.Background(), document.UpsertInput{
		Key:  document.Key{Scope: document.ScopeWorkspace, WorkspaceID: ws.ID, Name: "note"},
		Type: document.TypeDocument, Body: "seeded body for the agent", CreatedBy: admin.ID,
	}, now); err != nil {
		t.Fatalf("seed note: %v", err)
	}
	skillBody := "---\nname: summarise\nkind: task\ntools: [document_get]\n---\n## Spec\nSummarise the workspace corpus.\n"
	if _, _, err := docStore.Upsert(context.Background(), document.UpsertInput{
		Key:  document.Key{Scope: document.ScopeWorkspace, WorkspaceID: ws.ID, Name: "summarise"},
		Type: document.TypeSkill, Body: skillBody, CreatedBy: admin.ID,
	}, now); err != nil {
		t.Fatalf("seed skill: %v", err)
	}
	sysSeed := document.NewSystemSeedStore(env.DB)
	if _, err := document.ReconcileSystemSeedTyped(context.Background(), sysSeed, docStore,
		document.TypeDocument, "agent-operating-prompt",
		"You are a workspace agent. Use the read-only tools, ground in results, produce the deliverable.",
		nil, "", "system:test", now); err != nil {
		t.Fatalf("seed agent prompt: %v", err)
	}

	// The scheduled agent-task config, written directly (interval below the
	// upsert floor — the scheduler reads the config, it does not re-validate).
	cfg := map[string]any{
		"task_skill":  map[string]string{"scope": "workspace", "name": "summarise"},
		"output_name": "corpus-summary-scheduled",
		"trigger":     "schedule",
		"executor":    "agent",
		"schedule":    map[string]int{"interval_seconds": 1},
	}
	body, _ := json.MarshalIndent(cfg, "", "  ")
	if _, _, err := docStore.Upsert(context.Background(), document.UpsertInput{
		Key:  document.Key{Scope: document.ScopeWorkspace, WorkspaceID: ws.ID, Name: "agent-task/sched"},
		Type: document.TypeDocument, Body: string(body), CreatedBy: admin.ID,
	}, now); err != nil {
		t.Fatalf("seed scheduled task: %v", err)
	}

	// Run the real scheduler: tick fast, 1s interval. First sight defers one
	// interval, then it runs each interval.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go verb.NewTaskScheduler(150*time.Millisecond, ledgerStore).Run(ctx)

	// Poll for at least two runs (proving cadence), up to a generous window.
	countTaskRuns := func() int {
		res, err := ledgerStore.List(context.Background(), ledger.ListFilter{WorkspaceID: ws.ID, Kind: "task_run", Limit: 100})
		if err != nil {
			t.Fatalf("ledger list: %v", err)
		}
		return len(res.Entries)
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if countTaskRuns() >= 2 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	cancel()

	runs := countTaskRuns()
	if runs < 2 {
		t.Fatalf("expected >= 2 scheduled runs on cadence, got %d ledger task_run rows", runs)
	}

	// The output document was produced by the scheduled runs.
	got, err := docStore.Get(context.Background(), document.Key{Scope: document.ScopeWorkspace, WorkspaceID: ws.ID, Name: "corpus-summary-scheduled"}, document.GetOptions{})
	if err != nil || len(got.Versions) == 0 {
		t.Fatalf("scheduled output document not produced: %v", err)
	}
	if got.Versions[0].Body != "AGENT SUMMARY" {
		t.Fatalf("output body = %q, want %q", got.Versions[0].Body, "AGENT SUMMARY")
	}
}
