//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestStoryDelete covers sty_868945ca:
//   - story_delete returns the deleted story body
//   - subsequent story_get returns not-found
//   - child stories have parent_id nulled (ON DELETE SET NULL)
//   - existing ledger entries survive (append-only invariant)
//   - delete missing id surfaces ErrNotFound
func TestStoryDelete(t *testing.T) {
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
	})

	ctx := context.Background()
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	ws, err := wsStore.Create(ctx, "", "delete-test-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	pj, err := pjStore.Create(ctx, project.CreateInput{
		WorkspaceID: ws.ID,
		Name:        "delete-test-pj",
	}, now)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	t.Run("round-trip + body returned", func(t *testing.T) {
		req, _ := json.Marshal(verb.DocumentUpsertRequest{
			Type:      "story",
			ProjectID: pj.ID,
			Name:      "delete me",
		})
		raw, err := verb.Dispatch(ctx, "document_upsert", req)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		var created verb.DocumentUpsertResponse
		_ = json.Unmarshal(raw, &created)
		s := created.Document

		delReq, _ := json.Marshal(verb.DocumentDeleteRequest{ID: s.ID})
		raw, err = verb.Dispatch(ctx, "document_delete", delReq)
		if err != nil {
			t.Fatalf("delete: %v", err)
		}
		var resp verb.DocumentDeleteResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if resp.Document.ID != s.ID || resp.Document.Name != "delete me" {
			t.Fatalf("returned story mismatch: %+v", resp.Document)
		}

		getReq, _ := json.Marshal(verb.DocumentGetRequest{ID: s.ID})
		_, err = verb.Dispatch(ctx, "document_get", getReq)
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("expected not-found after delete, got %v", err)
		}
	})

	t.Run("children get parent_id nulled", func(t *testing.T) {
		parentRaw, err := verb.Dispatch(ctx, "document_upsert", mustJSON(t, verb.DocumentUpsertRequest{
			Type: "story", ProjectID: pj.ID, Name: "parent epic",
		}))
		if err != nil {
			t.Fatalf("create parent: %v", err)
		}
		var parentResp verb.DocumentUpsertResponse
		_ = json.Unmarshal(parentRaw, &parentResp)
		parent := parentResp.Document

		parentIDStr := parent.ID
		childRaw, err := verb.Dispatch(ctx, "document_upsert", mustJSON(t, verb.DocumentUpsertRequest{
			Type: "story", ProjectID: pj.ID, ParentID: &parentIDStr, Name: "child story",
		}))
		if err != nil {
			t.Fatalf("create child: %v", err)
		}
		var childResp verb.DocumentUpsertResponse
		_ = json.Unmarshal(childRaw, &childResp)
		child := childResp.Document
		if child.ParentID != parent.ID {
			t.Fatalf("child parent_id pre-condition: got %q want %q", child.ParentID, parent.ID)
		}

		if _, err := verb.Dispatch(ctx, "document_delete", mustJSON(t, verb.DocumentDeleteRequest{
			ID: parent.ID,
		})); err != nil {
			t.Fatalf("delete parent: %v", err)
		}

		refetched, err := docStore.GetByID(ctx, child.ID)
		if err != nil {
			t.Fatalf("refetch child: %v", err)
		}
		if refetched.ParentID != "" {
			t.Fatalf("child parent_id not nulled: got %q", refetched.ParentID)
		}
	})

	t.Run("ledger entries survive deletion", func(t *testing.T) {
		raw, err := verb.Dispatch(ctx, "document_upsert", mustJSON(t, verb.DocumentUpsertRequest{
			Type: "story", ProjectID: pj.ID, Name: "audit target",
		}))
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		var sResp verb.DocumentUpsertResponse
		_ = json.Unmarshal(raw, &sResp)
		s := sResp.Document

		before, err := ledStore.ListByStory(ctx, s.ID, "")
		if err != nil || len(before) == 0 {
			t.Fatalf("pre-condition: ledger list: %v / %d", err, len(before))
		}

		if _, err := verb.Dispatch(ctx, "document_delete", mustJSON(t, verb.DocumentDeleteRequest{
			ID: s.ID,
		})); err != nil {
			t.Fatalf("delete: %v", err)
		}

		after, err := ledStore.ListByStory(ctx, s.ID, "")
		if err != nil {
			t.Fatalf("post-delete list: %v", err)
		}
		if len(after) != len(before) {
			t.Fatalf("ledger entries lost: before=%d after=%d", len(before), len(after))
		}
	})

	t.Run("delete missing id returns not-found", func(t *testing.T) {
		_, err := verb.Dispatch(ctx, "document_delete", mustJSON(t, verb.DocumentDeleteRequest{
			ID: "sty_deadbeef",
		}))
		if err == nil {
			t.Fatalf("expected error for missing id")
		}
		if !errors.Is(err, document.ErrNotFound) && !strings.Contains(err.Error(), "not found") {
			t.Fatalf("expected not-found, got %v", err)
		}
	})
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
