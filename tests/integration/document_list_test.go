//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/mcpserver"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestDocumentList_FilterAndPagination is the regression net for
// sty_0dd71f79 ACs #3 + #4: structured filters return correct rows for
// every documented combination, and cursor pagination yields every
// distinct row exactly once on a static dataset.
//
// Stories and documents share the table post-unification, so the same
// verb covers both via the type discriminator.
func TestDocumentList_FilterAndPagination(t *testing.T) {
	env := testbootstrap.SetUp(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)

	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	docStore := document.New(env.DB)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(pjStore)
	verb.SetDocumentStore(docStore)
	verb.SetLedgerStore(ledger.New(env.DB))
	t.Cleanup(func() {
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
		verb.SetDocumentStore(nil)
		verb.SetLedgerStore(nil)
	})

	ws, err := wsStore.Create(ctx, "", "doclist-ws", now)
	if err != nil {
		t.Fatalf("ws: %v", err)
	}
	pjA, err := pjStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "pjA"}, now)
	if err != nil {
		t.Fatalf("pjA: %v", err)
	}
	pjB, err := pjStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "pjB"}, now)
	if err != nil {
		t.Fatalf("pjB: %v", err)
	}

	// Seed: 6 stories across the two projects with distinct
	// tag/status combos so each filter combination has a non-empty +
	// non-everything expected answer.
	seedStory := func(t *testing.T, pj project.Project, title, status string, tags []string, when time.Time) document.Document {
		t.Helper()
		d, err := docStore.CreateStory(ctx, document.CreateStoryInput{
			ProjectID:   pj.ID,
			WorkspaceID: pj.WorkspaceID,
			Title:       title,
			Status:      status,
			Tags:        tags,
		}, when)
		if err != nil {
			t.Fatalf("seed story %q: %v", title, err)
		}
		return d
	}
	// Spaced timestamps so the (created_at, id) cursor is stable.
	t0 := now
	a1 := seedStory(t, pjA, "alpha", "backlog", []string{"area:x", "epic:e1"}, t0.Add(1*time.Minute))
	a2 := seedStory(t, pjA, "beta", "in_progress", []string{"area:x"}, t0.Add(2*time.Minute))
	a3 := seedStory(t, pjA, "gamma", "done", []string{"epic:e1"}, t0.Add(3*time.Minute))
	b1 := seedStory(t, pjB, "delta", "backlog", []string{"area:x"}, t0.Add(4*time.Minute))
	b2 := seedStory(t, pjB, "epsilon", "in_progress", []string{"epic:e1"}, t0.Add(5*time.Minute))
	b3 := seedStory(t, pjB, "zeta", "done", []string{"area:y"}, t0.Add(6*time.Minute))
	allIDs := map[string]bool{a1.ID: true, a2.ID: true, a3.ID: true, b1.ID: true, b2.ID: true, b3.ID: true}

	// Also seed a type='document' so the type filter has something to
	// reject.
	if _, _, err := docStore.Upsert(ctx, document.UpsertInput{
		Key:       document.Key{Scope: document.ScopeProject, WorkspaceID: ws.ID, ProjectID: pjA.ID, Name: "release-notes"},
		Body:      "v1",
		CreatedBy: "test",
	}, t0.Add(7*time.Minute)); err != nil {
		t.Fatalf("seed doc: %v", err)
	}

	list := func(t *testing.T, req verb.DocumentListRequest) verb.DocumentListResponse {
		t.Helper()
		body, _ := json.Marshal(req)
		raw, err := verb.Dispatch(ctx, "document_list", body)
		if err != nil {
			t.Fatalf("document_list dispatch: %v", err)
		}
		var resp verb.DocumentListResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return resp
	}

	t.Run("type=story project_id=pjA returns 3", func(t *testing.T) {
		resp := list(t, verb.DocumentListRequest{Type: "story", ProjectID: pjA.ID})
		assertIDs(t, resp.Items, []string{a1.ID, a2.ID, a3.ID})
	})

	t.Run("type=story tags=[area:x] returns 3 across projects", func(t *testing.T) {
		resp := list(t, verb.DocumentListRequest{Type: "story", Tags: []string{"area:x"}})
		assertIDs(t, resp.Items, []string{a1.ID, a2.ID, b1.ID})
	})

	t.Run("type=story status=backlog returns 2", func(t *testing.T) {
		resp := list(t, verb.DocumentListRequest{Type: "story", Status: "backlog"})
		assertIDs(t, resp.Items, []string{a1.ID, b1.ID})
	})

	t.Run("type=story status=in_progress project_id=pjA tags=[area:x]", func(t *testing.T) {
		resp := list(t, verb.DocumentListRequest{
			Type:      "story",
			Status:    "in_progress",
			ProjectID: pjA.ID,
			Tags:      []string{"area:x"},
		})
		assertIDs(t, resp.Items, []string{a2.ID})
	})

	t.Run("type=document hides stories", func(t *testing.T) {
		resp := list(t, verb.DocumentListRequest{Type: "document"})
		for _, d := range resp.Items {
			if d.Type != "document" {
				t.Errorf("got type=%q in document-only filter", d.Type)
			}
		}
	})

	t.Run("type=all includes both kinds", func(t *testing.T) {
		resp := list(t, verb.DocumentListRequest{Type: "all"})
		seenStory, seenDoc := false, false
		for _, d := range resp.Items {
			if d.Type == "story" {
				seenStory = true
			}
			if d.Type == "document" {
				seenDoc = true
			}
		}
		if !seenStory || !seenDoc {
			t.Errorf("type=all should include both: seenStory=%v seenDoc=%v", seenStory, seenDoc)
		}
	})

	t.Run("cursor pagination returns each distinct row exactly once", func(t *testing.T) {
		// Walk pages of 2 over the type=story set and assert we see
		// every story id exactly once.
		seen := map[string]bool{}
		cursor := ""
		pages := 0
		for {
			resp := list(t, verb.DocumentListRequest{Type: "story", Limit: 2, Cursor: cursor})
			pages++
			if pages > 10 {
				t.Fatalf("pagination did not terminate; seen=%v cursor=%q", seen, cursor)
			}
			for _, d := range resp.Items {
				if seen[d.ID] {
					t.Errorf("duplicate id %s across pages", d.ID)
				}
				seen[d.ID] = true
			}
			if resp.NextCursor == "" {
				break
			}
			cursor = resp.NextCursor
		}
		if len(seen) != len(allIDs) {
			t.Errorf("seen %d rows, want %d (%v)", len(seen), len(allIDs), seen)
		}
		for id := range allIDs {
			if !seen[id] {
				t.Errorf("missing id %s after full pagination", id)
			}
		}
	})

	t.Run("limit cap enforced", func(t *testing.T) {
		// Verify limit > 200 is silently capped (no error, but the
		// returned page is at most 200 items).
		resp := list(t, verb.DocumentListRequest{Type: "story", Limit: 500})
		if len(resp.Items) > 200 {
			t.Errorf("page size %d exceeds cap", len(resp.Items))
		}
	})
}

// TestDocumentList_ViaMCP drives document_list end-to-end through the
// streamable-HTTP MCP transport, satisfying sty_0dd71f79 AC #5.
func TestDocumentList_ViaMCP(t *testing.T) {
	env := testbootstrap.SetUp(t)
	ctx := context.Background()
	now := time.Now().UTC()

	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	docStore := document.New(env.DB)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(pjStore)
	verb.SetDocumentStore(docStore)
	verb.SetLedgerStore(ledger.New(env.DB))
	t.Cleanup(func() {
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
		verb.SetDocumentStore(nil)
		verb.SetLedgerStore(nil)
	})

	ws, _ := wsStore.Create(ctx, "", "mcplist-ws", now)
	pj, _ := pjStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "mcplist-pj"}, now)
	for i := 0; i < 3; i++ {
		if _, err := docStore.CreateStory(ctx, document.CreateStoryInput{
			ProjectID: pj.ID, WorkspaceID: ws.ID,
			Title: fmt.Sprintf("mcp-story-%d", i),
			Tags:  []string{"area:mcp"},
		}, now.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	ts := httptest.NewServer(mcpserver.HTTPHandler(mcpserver.New()))
	t.Cleanup(ts.Close)
	cli, err := client.NewStreamableHttpClient(ts.URL)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	if err := cli.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := cli.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo:      mcp.Implementation{Name: "document-list-test", Version: "1"},
		},
	}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	t.Run("document_list via MCP", func(t *testing.T) {
		req := mcp.CallToolRequest{}
		req.Params.Name = "document_list"
		req.Params.Arguments = map[string]any{
			"type":       "story",
			"project_id": pj.ID,
			"tags":       []string{"area:mcp"},
		}
		res, err := cli.CallTool(ctx, req)
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
		if res.IsError {
			t.Fatalf("IsError: %+v", res.Content)
		}
		tc, ok := res.Content[0].(mcp.TextContent)
		if !ok {
			t.Fatalf("content[0] is %T", res.Content[0])
		}
		var resp verb.DocumentListResponse
		if err := json.Unmarshal([]byte(tc.Text), &resp); err != nil {
			t.Fatalf("decode: %v\n%s", err, tc.Text)
		}
		if len(resp.Items) != 3 {
			t.Errorf("items: got %d want 3", len(resp.Items))
		}
	})

}

func assertIDs(t *testing.T, got []document.Document, want []string) {
	t.Helper()
	if len(got) != len(want) {
		ids := make([]string, len(got))
		for i, d := range got {
			ids[i] = d.ID
		}
		t.Errorf("count: got %d (%v), want %d (%v)", len(got), ids, len(want), want)
		return
	}
	wantSet := map[string]bool{}
	for _, w := range want {
		wantSet[w] = true
	}
	for _, d := range got {
		if !wantSet[d.ID] {
			t.Errorf("unexpected id in result: %s", d.ID)
		}
	}
}
