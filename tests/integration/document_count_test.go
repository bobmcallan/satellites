//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestDocumentCount exercises sty_cc20c0f5 — count rows matching the
// same filter shape document_list accepts, without paging. Pairs with
// document_list for total-row indicators in the portal.
func TestDocumentCount(t *testing.T) {
	env := testbootstrap.SetUp(t)
	testbootstrap.Reset(t, env)

	authStore := auth.New(env.DB)
	if err := authStore.DevSeed(context.Background()); err != nil {
		t.Fatalf("dev seed: %v", err)
	}
	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	docStore := document.New(env.DB)
	verb.SetAuthStore(authStore)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(pjStore)
	verb.SetDocumentStore(docStore)
	t.Cleanup(func() {
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
		verb.SetDocumentStore(nil)
	})

	ctx := context.Background()
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	ws, err := wsStore.Create(ctx, "", "count-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	pjA, err := pjStore.Create(ctx, project.CreateInput{
		WorkspaceID: ws.ID,
		Name:        "count-project-a",
	}, now)
	if err != nil {
		t.Fatalf("create project A: %v", err)
	}
	pjB, err := pjStore.Create(ctx, project.CreateInput{
		WorkspaceID: ws.ID,
		Name:        "count-project-b",
	}, now)
	if err != nil {
		t.Fatalf("create project B: %v", err)
	}

	// Seed 12 stories across both projects with mixed tags + statuses.
	type seed struct {
		Project string
		Name    string
		Status  string
		Tags    []string
	}
	seeds := []seed{
		// project A — 7 stories
		{pjA.ID, "a-1", "backlog", []string{"area:x"}},
		{pjA.ID, "a-2", "backlog", []string{"area:x"}},
		{pjA.ID, "a-3", "ready", []string{"area:y"}},
		{pjA.ID, "a-4", "done", []string{"area:x"}},
		{pjA.ID, "a-5", "done", []string{"area:y"}},
		{pjA.ID, "a-6", "in_progress", []string{}},
		{pjA.ID, "a-7", "review", []string{"area:x"}},
		// project B — 5 stories
		{pjB.ID, "b-1", "backlog", []string{"area:x"}},
		{pjB.ID, "b-2", "ready", []string{"area:y"}},
		{pjB.ID, "b-3", "done", []string{"area:y"}},
		{pjB.ID, "b-4", "done", []string{}},
		{pjB.ID, "b-5", "backlog", []string{"area:x"}},
	}
	for _, s := range seeds {
		st := s.Status
		tg := append([]string(nil), s.Tags...)
		req, _ := json.Marshal(verb.DocumentUpsertRequest{
			Type:      "story",
			ProjectID: s.Project,
			Name:      s.Name,
			Status:    &st,
			Tags:      &tg,
		})
		if _, err := verb.Dispatch(ctx, "document_upsert", req); err != nil {
			t.Fatalf("seed %s: %v", s.Name, err)
		}
	}

	count := func(t *testing.T, reqJSON string) int {
		t.Helper()
		raw, err := verb.Dispatch(ctx, "document_count", json.RawMessage(reqJSON))
		if err != nil {
			t.Fatalf("dispatch document_count: %v", err)
		}
		var resp verb.DocumentCountResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		return resp.Count
	}

	t.Run("type=story project=A returns 7", func(t *testing.T) {
		if n := count(t, `{"type":"story","project_id":"`+pjA.ID+`"}`); n != 7 {
			t.Errorf("got %d want 7", n)
		}
	})

	t.Run("type=story status=done returns 4 across both projects", func(t *testing.T) {
		if n := count(t, `{"type":"story","status":"done"}`); n != 4 {
			t.Errorf("got %d want 4", n)
		}
	})

	t.Run("type=story tags=[area:x] returns 6 across both projects", func(t *testing.T) {
		if n := count(t, `{"type":"story","tags":["area:x"]}`); n != 6 {
			t.Errorf("got %d want 6", n)
		}
	})

	t.Run("type=story tags=[area:x] project=A returns 4", func(t *testing.T) {
		if n := count(t, `{"type":"story","tags":["area:x"],"project_id":"`+pjA.ID+`"}`); n != 4 {
			t.Errorf("got %d want 4", n)
		}
	})

	t.Run("no filter returns 12 stories total", func(t *testing.T) {
		if n := count(t, `{"type":"story"}`); n != 12 {
			t.Errorf("got %d want 12", n)
		}
	})

	t.Run("matches document_list page count when filter is identical", func(t *testing.T) {
		// Sanity: walk document_list with the same filter, verify the
		// total row count matches document_count. Catches WHERE-clause
		// drift between the two helpers.
		listReq, _ := json.Marshal(verb.DocumentListRequest{
			Type:      "story",
			ProjectID: pjA.ID,
			Limit:     200,
		})
		listRaw, err := verb.Dispatch(ctx, "document_list", listReq)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		var list verb.DocumentListResponse
		_ = json.Unmarshal(listRaw, &list)
		got := count(t, `{"type":"story","project_id":"`+pjA.ID+`"}`)
		if got != len(list.Items) {
			t.Errorf("count %d != list len %d — WHERE clause drift", got, len(list.Items))
		}
	})
}
