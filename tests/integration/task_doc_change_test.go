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

// waitUntil polls cond until true or a deadline, failing the test on timeout.
func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestTaskScheduler_OnDocumentChange drives the real scheduler over Postgres
// with a fake agent model: an on_document_change task establishes a baseline (no
// run), runs once when a corpus document is upserted, never re-fires on its own
// output (no self-trigger loop), and coalesces two rapid edits into a single run
// (sty_835f6d19). Time-driven, non-UI. The strict coalesce invariant is also
// covered deterministically by TestTaskScheduler_DocChange_Coalesce (unit).
func TestTaskScheduler_OnDocumentChange(t *testing.T) {
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
	ws, err := wsStore.Create(context.Background(), admin.ID, "react-ws", time.Now())
	if err != nil {
		t.Fatalf("create ws: %v", err)
	}
	if err := wsStore.AddMember(context.Background(), ws.ID, admin.ID, workspace.RoleAdmin, admin.ID, time.Now()); err != nil {
		t.Fatalf("add member: %v", err)
	}

	upsertDoc := func(name, body string) {
		t.Helper()
		if _, _, err := docStore.Upsert(context.Background(), document.UpsertInput{
			Key:  document.Key{Scope: document.ScopeWorkspace, WorkspaceID: ws.ID, Name: name},
			Type: document.TypeDocument, Body: body, CreatedBy: admin.ID,
		}, time.Now().UTC()); err != nil {
			t.Fatalf("upsert %s: %v", name, err)
		}
	}

	now := time.Now().UTC()
	upsertDoc("note", "seeded body for the agent")
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

	// An on_document_change task; output excluded from the change signal.
	cfg := map[string]any{
		"task_skill":  map[string]string{"scope": "workspace", "name": "summarise"},
		"output_name": "corpus-summary-reactive",
		"trigger":     "on_document_change",
		"executor":    "agent",
	}
	body, _ := json.MarshalIndent(cfg, "", "  ")
	if _, _, err := docStore.Upsert(context.Background(), document.UpsertInput{
		Key:  document.Key{Scope: document.ScopeWorkspace, WorkspaceID: ws.ID, Name: "agent-task/react"},
		Type: document.TypeDocument, Body: string(body), CreatedBy: admin.ID,
	}, now); err != nil {
		t.Fatalf("seed reactive task: %v", err)
	}

	countRuns := func() int {
		res, err := ledgerStore.List(context.Background(), ledger.ListFilter{WorkspaceID: ws.ID, Kind: "task_run", Limit: 100})
		if err != nil {
			t.Fatalf("ledger list: %v", err)
		}
		return len(res.Entries)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go verb.NewTaskScheduler(150*time.Millisecond, ledgerStore).Run(ctx)

	// Baseline: with no corpus change, several ticks must NOT run the task.
	time.Sleep(700 * time.Millisecond)
	if n := countRuns(); n != 0 {
		t.Fatalf("baseline: task ran %d times with no corpus change", n)
	}

	// A corpus change → exactly one run, then no self-trigger re-fire.
	upsertDoc("extra", "a new corpus document")
	waitUntil(t, "one run after change", func() bool { return countRuns() == 1 })
	time.Sleep(700 * time.Millisecond) // several ticks; the output write must not re-fire
	if n := countRuns(); n != 1 {
		t.Fatalf("self-trigger / over-fire: expected 1 run after one change, got %d", n)
	}
	got, err := docStore.Get(context.Background(), document.Key{Scope: document.ScopeWorkspace, WorkspaceID: ws.ID, Name: "corpus-summary-reactive"}, document.GetOptions{})
	if err != nil || len(got.Versions) == 0 || got.Versions[0].Body != "AGENT SUMMARY" {
		t.Fatalf("reactive output not produced: err=%v versions=%d", err, len(got.Versions))
	}

	// Two rapid edits before the next tick coalesce into a single additional run.
	upsertDoc("burst-a", "edit one")
	upsertDoc("burst-b", "edit two")
	waitUntil(t, "coalesced run after burst", func() bool { return countRuns() == 2 })
	time.Sleep(700 * time.Millisecond) // let further ticks settle
	if n := countRuns(); n != 2 {
		t.Fatalf("coalesce failed: expected 2 total runs (1 + coalesced burst), got %d", n)
	}
}
