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
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestLedgerVerbs exercises the full ledger surface:
//   - ledger_append round-trip
//   - ledger_list ordering (oldest-first by created_at)
//   - ledger_list kind filter
//   - story_create auto-emits kind=story_created
//   - story_update auto-emits kind=story_updated with diff payload
//
// Covers sty_5baec353 AC#2 (verbs registered), AC#3+#4 (auto-emit),
// AC#5 (filter by kind), AC#6 (round-trip + ordering).
func TestLedgerVerbs(t *testing.T) {
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
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	ws, err := wsStore.Create(ctx, "", "ledger-test-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	pj, err := pjStore.Create(ctx, project.CreateInput{
		WorkspaceID: ws.ID,
		Name:        "ledger-test-pj",
	}, now)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	t.Run("story create via document_upsert auto-appends kind=story_created", func(t *testing.T) {
		req, _ := json.Marshal(verb.DocumentUpsertRequest{
			Type:      "story",
			ProjectID: pj.ID,
			Name:      "ledger smoke",
		})
		raw, err := verb.Dispatch(ctx, "document_upsert", req)
		if err != nil {
			t.Fatalf("document_upsert (story create): %v", err)
		}
		var resp verb.DocumentUpsertResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		s := resp.Document

		entries, err := ledStore.ListByStory(ctx, s.ID, "")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("expected 1 entry, got %d", len(entries))
		}
		if entries[0].Kind != ledger.KindStoryCreated {
			t.Fatalf("kind = %q, want %q", entries[0].Kind, ledger.KindStoryCreated)
		}
		// Ledger payload is a StoryEnvelope (so reviewers + portal
		// consumers see the title/body shape they always have).
		var payload verb.StoryEnvelope
		if err := json.Unmarshal(entries[0].Payload, &payload); err != nil {
			t.Fatalf("payload not a story envelope: %v", err)
		}
		if payload.ID != s.ID || payload.Title != "ledger smoke" {
			t.Fatalf("payload mismatch: %+v", payload)
		}
	})

	t.Run("story update via document_upsert auto-appends kind=story_updated with diff", func(t *testing.T) {
		req, _ := json.Marshal(verb.DocumentUpsertRequest{
			Type:      "story",
			ProjectID: pj.ID,
			Name:      "diff target",
		})
		raw, err := verb.Dispatch(ctx, "document_upsert", req)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		var created verb.DocumentUpsertResponse
		_ = json.Unmarshal(raw, &created)
		s := created.Document

		// Mutate status + title (Name) by id-addressed upsert.
		newStatus := "in_progress"
		upd, _ := json.Marshal(verb.DocumentUpsertRequest{
			ID:     s.ID,
			Name:   "diff target (renamed)",
			Status: &newStatus,
		})
		if _, err := verb.Dispatch(ctx, "document_upsert", upd); err != nil {
			t.Fatalf("update: %v", err)
		}

		entries, err := ledStore.ListByStory(ctx, s.ID, ledger.KindStoryUpdated)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("expected 1 update entry, got %d", len(entries))
		}
		var diff map[string]map[string]any
		if err := json.Unmarshal(entries[0].Payload, &diff); err != nil {
			t.Fatalf("diff payload: %v", err)
		}
		if _, ok := diff["title"]; !ok {
			t.Fatalf("title diff missing: %+v", diff)
		}
		if _, ok := diff["status"]; !ok {
			t.Fatalf("status diff missing: %+v", diff)
		}
		if diff["title"]["after"] != "diff target (renamed)" {
			t.Fatalf("title.after = %v, want %q", diff["title"]["after"], "diff target (renamed)")
		}
	})

	t.Run("ledger_append + ledger_list round-trip + ordering", func(t *testing.T) {
		req, _ := json.Marshal(verb.DocumentUpsertRequest{
			Type:      "story",
			ProjectID: pj.ID,
			Name:      "round-trip target",
		})
		raw, _ := verb.Dispatch(ctx, "document_upsert", req)
		var createResp verb.DocumentUpsertResponse
		_ = json.Unmarshal(raw, &createResp)
		s := createResp.Document

		// Append two comments + one review_finding. Sleep nudges the
		// created_at timestamps so List ordering is deterministic.
		appendReq := func(kind, body string) {
			r, _ := json.Marshal(verb.LedgerAppendRequest{
				StoryID: s.ID,
				Kind:    kind,
				Actor:   "test:user",
				Body:    body,
			})
			if _, err := verb.Dispatch(ctx, "ledger_append", r); err != nil {
				t.Fatalf("ledger_append: %v", err)
			}
			time.Sleep(2 * time.Millisecond)
		}
		appendReq(ledger.KindComment, "first comment")
		appendReq(ledger.KindReviewFinding, "missing AC")
		appendReq(ledger.KindComment, "follow-up")

		// List unfiltered — expect story_created + 3 appended in order.
		listReq, _ := json.Marshal(verb.LedgerListRequest{StoryID: s.ID})
		listRaw, err := verb.Dispatch(ctx, "ledger_list", listReq)
		if err != nil {
			t.Fatalf("ledger_list: %v", err)
		}
		var resp verb.LedgerListResponse
		if err := json.Unmarshal(listRaw, &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(resp.Entries) != 4 {
			t.Fatalf("expected 4 entries, got %d", len(resp.Entries))
		}
		wantOrder := []string{
			ledger.KindStoryCreated,
			ledger.KindComment,
			ledger.KindReviewFinding,
			ledger.KindComment,
		}
		for i, want := range wantOrder {
			if resp.Entries[i].Kind != want {
				t.Fatalf("entries[%d].Kind = %q, want %q", i, resp.Entries[i].Kind, want)
			}
		}
		for i := 1; i < len(resp.Entries); i++ {
			if resp.Entries[i].CreatedAt.Before(resp.Entries[i-1].CreatedAt) {
				t.Fatalf("ordering violated at %d: %v before %v",
					i, resp.Entries[i].CreatedAt, resp.Entries[i-1].CreatedAt)
			}
		}

		// Kind filter — only comments.
		filtered, _ := json.Marshal(verb.LedgerListRequest{StoryID: s.ID, Kind: ledger.KindComment})
		raw, err = verb.Dispatch(ctx, "ledger_list", filtered)
		if err != nil {
			t.Fatalf("filtered list: %v", err)
		}
		_ = json.Unmarshal(raw, &resp)
		if len(resp.Entries) != 2 {
			t.Fatalf("expected 2 comment entries, got %d", len(resp.Entries))
		}
		for _, e := range resp.Entries {
			if e.Kind != ledger.KindComment {
				t.Fatalf("filter leak: %q", e.Kind)
			}
		}
	})

	t.Run("append-only — direct UPDATE on evidence is rejected", func(t *testing.T) {
		req, _ := json.Marshal(verb.DocumentUpsertRequest{
			Type:      "story",
			ProjectID: pj.ID,
			Name:      "append-only target",
		})
		raw, _ := verb.Dispatch(ctx, "document_upsert", req)
		var resp verb.DocumentUpsertResponse
		_ = json.Unmarshal(raw, &resp)
		s := resp.Document

		entries, err := ledStore.ListByStory(ctx, s.ID, "")
		if err != nil || len(entries) == 0 {
			t.Fatalf("setup: list: %v / %d", err, len(entries))
		}
		_, err = env.DB.Exec(`UPDATE evidence SET body = 'tampered' WHERE id = $1`, entries[0].ID)
		if err == nil {
			t.Fatalf("expected DB to reject UPDATE on evidence")
		}
	})
}
