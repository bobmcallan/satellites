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
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestTaskFlowReviewerGated proves a TASK flows through its workflow exactly like
// a story (epic:task-reviewer-parity): ready → running → complete, where the
// AGENT does the work (a document_upsert of the body) and the status moves ONLY
// when a reviewer enacts a status_transition ledger row. The reviewer judgments
// themselves run via claude -p (non-deterministic) so the test simulates the
// gate ENACTMENT (the ledger rows the gate writes), not the judgment — which is
// exactly the part that must be deterministic: the status machinery.
func TestTaskFlowReviewerGated(t *testing.T) {
	env := testbootstrap.SetUp(t)
	testbootstrap.Reset(t, env)

	authStore := auth.New(env.DB)
	if err := authStore.DevSeed(context.Background()); err != nil {
		t.Fatalf("dev seed: %v", err)
	}
	admin, err := authStore.GetUserByEmail(context.Background(), auth.DevAdminEmail)
	if err != nil {
		t.Fatalf("lookup admin: %v", err)
	}
	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	docStore := document.New(env.DB)
	ledStore := ledger.New(env.DB)
	verb.SetAuthStore(authStore)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(pjStore)
	verb.SetDocumentStore(docStore)
	verb.SetLedgerStore(ledStore)
	t.Cleanup(func() {
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
		verb.SetDocumentStore(nil)
		verb.SetLedgerStore(nil)
	})

	ctx := authWithUser(context.Background(), admin)
	now := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	ws, err := wsStore.Create(ctx, "", "task-flow-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	pj, err := pjStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "task-flow-pj"}, now)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	// Create a top-level task — it defaults to `ready`.
	taskID := createTaskFlow(t, ctx, pj.ID, "stale-epic backlog scan",
		"## Task\nReview the backlog for stale epics.\n## Output\nA report.\n## Verification\nA one-line verdict.")
	if got := statusOf(t, ctx, taskID); got != "ready" {
		t.Fatalf("new task status = %q, want ready", got)
	}

	// The AGENT does work — a document_upsert of the body. This MUST NOT move
	// status: the work path never self-advances.
	upsertBodyFlow(t, ctx, taskID, "## Task\n...\n## Report\n(working)")
	if got := statusOf(t, ctx, taskID); got != "ready" {
		t.Fatalf("after a work body-upsert, status = %q, want still ready (work must not move status)", got)
	}

	// Entry reviewer ENACTS: review_accept + status_transition → running.
	enactFlow(t, ctx, taskID, pj.ID, "ready", "running", "satellites-task-upsert-review")
	if got := statusOf(t, ctx, taskID); got != "running" {
		t.Fatalf("after entry-gate enactment, status = %q, want running", got)
	}

	// Agent works again (writes the report) — still running.
	upsertBodyFlow(t, ctx, taskID, "## Task\n...\n## Report\nScanned N epics; verdict: clean.")
	if got := statusOf(t, ctx, taskID); got != "running" {
		t.Fatalf("after the work report-upsert, status = %q, want still running", got)
	}

	// Exit reviewer ENACTS: review_accept + status_transition → complete.
	enactFlow(t, ctx, taskID, pj.ID, "running", "complete", "satellites-task-report-review")
	if got := statusOf(t, ctx, taskID); got != "complete" {
		t.Fatalf("after exit-gate enactment, status = %q, want complete", got)
	}
}

func createTaskFlow(t *testing.T, ctx context.Context, projectID, name, body string) string {
	t.Helper()
	req, _ := json.Marshal(verb.DocumentUpsertRequest{Type: "task", ProjectID: projectID, Name: name, Body: body})
	raw, err := verb.Dispatch(ctx, "document_upsert", req)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	var resp verb.DocumentUpsertResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode task: %v", err)
	}
	return resp.Document.ID
}

func upsertBodyFlow(t *testing.T, ctx context.Context, taskID, body string) {
	t.Helper()
	req, _ := json.Marshal(verb.DocumentUpsertRequest{ID: taskID, Body: body})
	if _, err := verb.Dispatch(ctx, "document_upsert", req); err != nil {
		t.Fatalf("work body-upsert: %v", err)
	}
}

// enactFlow simulates a reviewer gate's enactment: the two ledger rows a gate
// writes on accept (review_accept then status_transition). The status_transition
// row is the sole status writer.
func enactFlow(t *testing.T, ctx context.Context, taskID, projectID, from, to, gate string) {
	t.Helper()
	acc, _ := json.Marshal(verb.LedgerAppendRequest{
		StoryID: taskID, ProjectID: projectID, Kind: "review_accept",
		Body:    "ok",
		Payload: json.RawMessage(`{"from_status":"` + from + `","to_status":"` + to + `","gate":"` + gate + `"}`),
	})
	if _, err := verb.Dispatch(ctx, "ledger_append", acc); err != nil {
		t.Fatalf("review_accept (%s): %v", gate, err)
	}
	st, _ := json.Marshal(verb.LedgerAppendRequest{
		StoryID: taskID, ProjectID: projectID, Kind: "status_transition",
		Body:    from + " → " + to,
		Payload: json.RawMessage(`{"from_status":"` + from + `","to_status":"` + to + `"}`),
	})
	if _, err := verb.Dispatch(ctx, "ledger_append", st); err != nil {
		t.Fatalf("status_transition (%s→%s): %v", from, to, err)
	}
}

func statusOf(t *testing.T, ctx context.Context, taskID string) string {
	t.Helper()
	req, _ := json.Marshal(verb.DocumentGetRequest{ID: taskID})
	raw, err := verb.Dispatch(ctx, "document_get", req)
	if err != nil {
		t.Fatalf("document_get: %v", err)
	}
	var resp verb.DocumentGetResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	return resp.Document.Status
}
