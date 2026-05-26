//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/story"
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
	stStore := story.New(env.DB)
	ledStore := ledger.New(env.DB)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(pjStore)
	verb.SetStoryStore(stStore)
	verb.SetLedgerStore(ledStore)
	t.Cleanup(func() {
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
		verb.SetStoryStore(nil)
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
		req, _ := json.Marshal(verb.StoryCreateRequest{
			ProjectID: pj.ID,
			Title:     "delete me",
		})
		raw, err := verb.Dispatch(ctx, "story_create", req)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		var s story.Story
		_ = json.Unmarshal(raw, &s)

		delReq, _ := json.Marshal(verb.StoryDeleteRequest{ID: s.ID})
		raw, err = verb.Dispatch(ctx, "story_delete", delReq)
		if err != nil {
			t.Fatalf("delete: %v", err)
		}
		var resp verb.StoryDeleteResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if resp.Story.ID != s.ID || resp.Story.Title != "delete me" {
			t.Fatalf("returned story mismatch: %+v", resp.Story)
		}

		getReq, _ := json.Marshal(verb.StoryGetRequest{ID: s.ID})
		_, err = verb.Dispatch(ctx, "story_get", getReq)
		if err == nil || !errors.Is(err, story.ErrNotFound) {
			t.Fatalf("expected ErrNotFound after delete, got %v", err)
		}
	})

	t.Run("children get parent_id nulled", func(t *testing.T) {
		parentRaw, err := verb.Dispatch(ctx, "story_create", mustJSON(t, verb.StoryCreateRequest{
			ProjectID: pj.ID, Title: "parent epic",
		}))
		if err != nil {
			t.Fatalf("create parent: %v", err)
		}
		var parent story.Story
		_ = json.Unmarshal(parentRaw, &parent)

		childRaw, err := verb.Dispatch(ctx, "story_create", mustJSON(t, verb.StoryCreateRequest{
			ProjectID: pj.ID, ParentID: parent.ID, Title: "child story",
		}))
		if err != nil {
			t.Fatalf("create child: %v", err)
		}
		var child story.Story
		_ = json.Unmarshal(childRaw, &child)
		if child.ParentID != parent.ID {
			t.Fatalf("child parent_id pre-condition: got %q want %q", child.ParentID, parent.ID)
		}

		if _, err := verb.Dispatch(ctx, "story_delete", mustJSON(t, verb.StoryDeleteRequest{
			ID: parent.ID,
		})); err != nil {
			t.Fatalf("delete parent: %v", err)
		}

		refetched, err := stStore.GetByID(ctx, child.ID)
		if err != nil {
			t.Fatalf("refetch child: %v", err)
		}
		if refetched.ParentID != "" {
			t.Fatalf("child parent_id not nulled: got %q", refetched.ParentID)
		}
	})

	t.Run("ledger entries survive deletion", func(t *testing.T) {
		raw, err := verb.Dispatch(ctx, "story_create", mustJSON(t, verb.StoryCreateRequest{
			ProjectID: pj.ID, Title: "audit target",
		}))
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		var s story.Story
		_ = json.Unmarshal(raw, &s)

		before, err := ledStore.List(ctx, s.ID, "")
		if err != nil || len(before) == 0 {
			t.Fatalf("pre-condition: ledger list: %v / %d", err, len(before))
		}

		if _, err := verb.Dispatch(ctx, "story_delete", mustJSON(t, verb.StoryDeleteRequest{
			ID: s.ID,
		})); err != nil {
			t.Fatalf("delete: %v", err)
		}

		after, err := ledStore.List(ctx, s.ID, "")
		if err != nil {
			t.Fatalf("post-delete list: %v", err)
		}
		if len(after) != len(before) {
			t.Fatalf("ledger entries lost: before=%d after=%d", len(before), len(after))
		}
	})

	t.Run("delete missing id returns not-found", func(t *testing.T) {
		_, err := verb.Dispatch(ctx, "story_delete", mustJSON(t, verb.StoryDeleteRequest{
			ID: "sty_deadbeef",
		}))
		if err == nil {
			t.Fatalf("expected error for missing id")
		}
		if !errors.Is(err, story.ErrNotFound) && !strings.Contains(err.Error(), "not found") {
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
