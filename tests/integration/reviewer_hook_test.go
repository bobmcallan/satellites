//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/reviewer"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// fakeReviewerClient returns a canned completion shaped like the
// production output schema. Used to assert end-to-end wiring
// (story_create → reviewer registry → ledger append) without
// reaching for the real LLM.
type fakeReviewerClient struct {
	resp string
}

func (f *fakeReviewerClient) Complete(_ context.Context, _ string, _ int, _ string) (string, error) {
	return f.resp, nil
}

// TestReviewerHook exercises the full reviewer surface:
//   - empty registry → no ledger findings (test mode default)
//   - loaded registry + stub client → kind=review_finding entries
//     appear under the story after story_create
//   - story_update also triggers a fresh review
//
// Covers sty_a19b031b AC#3 (wired into create+update), AC#4
// (findings via ledger verbs), AC#5 (dispatch path exercised
// synchronously via SetReviewerDispatchForTest), AC#6 (stub
// substitution).
func TestReviewerHook(t *testing.T) {
	env := testbootstrap.SetUp(t)
	testbootstrap.Reset(t, env)

	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	docStore := document.New(env.DB)
	ledStore := ledger.New(env.DB)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(pjStore)
	verb.SetDocumentStore(docStore)
	verb.SetLedgerStore(ledStore)
	t.Cleanup(func() {
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
		verb.SetDocumentStore(nil)
		verb.SetLedgerStore(nil)
		verb.SetReviewerRegistry(nil)
		verb.SetReviewerDispatchForTest(nil)
	})

	ctx := context.Background()
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	ws, err := wsStore.Create(ctx, "", "reviewer-test-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	pj, err := pjStore.Create(ctx, project.CreateInput{
		WorkspaceID: ws.ID,
		Name:        "reviewer-test-pj",
	}, now)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	// Synchronous dispatch so assertions don't race the goroutine.
	verb.SetReviewerDispatchForTest(func(ctx context.Context, s verb.StoryEnvelope) {
		runReviewersSyncForTest(ctx, s)
	})

	t.Run("empty registry produces no findings", func(t *testing.T) {
		verb.SetReviewerRegistry(reviewer.NewRegistry(nil, nil))
		req, _ := json.Marshal(verb.DocumentUpsertRequest{
			Type:      "story",
			ProjectID: pj.ID,
			Name:      "no-reviewer story",
		})
		raw, err := verb.Dispatch(ctx, "document_upsert", req)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		var resp verb.DocumentUpsertResponse
		_ = json.Unmarshal(raw, &resp)
		entries, err := ledStore.List(ctx, resp.Document.ID, ledger.KindReviewFinding)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(entries) != 0 {
			t.Fatalf("expected 0 findings, got %d", len(entries))
		}
	})

	t.Run("stub client produces ledger findings after create + update", func(t *testing.T) {
		client := &fakeReviewerClient{
			resp: `{"findings":[
                {"severity":"warn","code":"vague_title","field":"title","message":"too short"},
                {"severity":"info","code":"missing_purpose","field":"body","message":"add a purpose paragraph"}
            ]}`,
		}
		defs := map[string]reviewer.Definition{
			"story_reviewer": {
				Name: "story_reviewer", Enabled: true, Model: "test-model", MaxTokens: 256,
				Body: "You are a reviewer.",
			},
		}
		verb.SetReviewerRegistry(reviewer.NewRegistry(defs, client))

		req, _ := json.Marshal(verb.DocumentUpsertRequest{
			Type:      "story",
			ProjectID: pj.ID,
			Name:      "hi",
		})
		raw, err := verb.Dispatch(ctx, "document_upsert", req)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		var resp verb.DocumentUpsertResponse
		_ = json.Unmarshal(raw, &resp)
		s := resp.Document

		entries, err := ledStore.List(ctx, s.ID, ledger.KindReviewFinding)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(entries) != 2 {
			t.Fatalf("expected 2 findings after create, got %d", len(entries))
		}
		// Update triggers another review pass.
		updReq, _ := json.Marshal(verb.DocumentUpsertRequest{
			ID:   s.ID,
			Name: "hi (renamed)",
		})
		if _, err := verb.Dispatch(ctx, "document_upsert", updReq); err != nil {
			t.Fatalf("update: %v", err)
		}
		entries, _ = ledStore.List(ctx, s.ID, ledger.KindReviewFinding)
		if len(entries) != 4 {
			t.Fatalf("expected 4 findings after update, got %d", len(entries))
		}
		// Payload carries the reviewer name + severity + code.
		var payload map[string]any
		if err := json.Unmarshal(entries[0].Payload, &payload); err != nil {
			t.Fatalf("payload: %v", err)
		}
		if payload["reviewer"] != "story_reviewer" {
			t.Fatalf("reviewer = %v", payload["reviewer"])
		}
		if payload["code"] != "vague_title" {
			t.Fatalf("code = %v", payload["code"])
		}
	})
}

// runReviewersSyncForTest mirrors the production spawn but blocks
// the caller. Tests need synchronous semantics — production needs
// the goroutine. The body diverges only on whether `go` is in
// front of the call.
func runReviewersSyncForTest(ctx context.Context, s verb.StoryEnvelope) {
	// Re-using the package's exported synchronous path keeps the
	// finding-shape logic single-sourced. The verb package exposes
	// it indirectly via SetReviewerDispatchForTest hooks.
	verb.RunReviewersSyncForTest(ctx, s)
}
