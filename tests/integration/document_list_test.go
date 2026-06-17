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

	t.Run("name_contains=et matches a substring across names", func(t *testing.T) {
		// "et" is a substring of beta + zeta (not a prefix of either).
		resp := list(t, verb.DocumentListRequest{Type: "story", NameContains: "et"})
		assertIDs(t, resp.Items, []string{a2.ID, b3.ID})
	})

	t.Run("name_contains is distinct from name_prefix", func(t *testing.T) {
		// No name starts with "et", so name_prefix returns nothing while
		// name_contains finds the substring matches — they are different filters.
		pref := list(t, verb.DocumentListRequest{Type: "story", NamePrefix: "et"})
		assertIDs(t, pref.Items, nil)
		sub := list(t, verb.DocumentListRequest{Type: "story", NameContains: "et"})
		assertIDs(t, sub.Items, []string{a2.ID, b3.ID})
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

// TestDocumentList_ExcludesTombstones is the regression net for
// sty_4148d3fa: a soft-deleted row must drop out of List by default (so it
// agrees with the name-addressed get, which returns not-found), still be
// reachable under status="all", and leave live rows untouched. Delete must
// also set the row-level documents.status to "deleted".
func TestDocumentList_ExcludesTombstones(t *testing.T) {
	env := testbootstrap.SetUp(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)

	docStore := document.New(env.DB)
	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	ws, err := wsStore.Create(ctx, "", "tombstone-ws", now)
	if err != nil {
		t.Fatalf("ws: %v", err)
	}
	pj, err := pjStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "tombstone-pj"}, now)
	if err != nil {
		t.Fatalf("pj: %v", err)
	}

	// Two project skills: one we delete, one that stays live.
	mk := func(name string) document.Document {
		d, _, err := docStore.Upsert(ctx, document.UpsertInput{
			Key:       document.Key{Scope: document.ScopeProject, WorkspaceID: ws.ID, ProjectID: pj.ID, Name: name},
			Type:      "skill",
			Body:      "---\nname: " + name + "\n---\nbody\n",
			CreatedBy: "test",
		}, now)
		if err != nil {
			t.Fatalf("seed skill %q: %v", name, err)
		}
		return d
	}
	doomed := mk("gone-skill")
	live := mk("kept-skill")

	delKey := document.Key{Scope: document.ScopeProject, WorkspaceID: ws.ID, ProjectID: pj.ID, Name: "gone-skill"}
	delDoc, _, err := docStore.Delete(ctx, delKey, "test", false, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	// AC3: the row-level status reflects the soft-delete.
	if delDoc.Status != string(document.StatusDeleted) {
		t.Errorf("after Delete, document.Status = %q, want %q", delDoc.Status, document.StatusDeleted)
	}

	filter := document.ListFilter{Type: "skill", Scope: document.ScopeProject, WorkspaceID: ws.ID, ProjectID: pj.ID}

	// AC1/AC2: default list omits the tombstone, keeps the live row.
	def, err := docStore.List(ctx, filter, document.ListOptions{})
	if err != nil {
		t.Fatalf("list default: %v", err)
	}
	ids := map[string]bool{}
	for _, d := range def.Items {
		ids[d.ID] = true
	}
	if ids[doomed.ID] {
		t.Errorf("default list returned the deleted row %s", doomed.ID)
	}
	if !ids[live.ID] {
		t.Errorf("default list dropped the live row %s", live.ID)
	}

	// status="all" still surfaces the tombstone.
	all, err := docStore.List(ctx, document.ListFilter{
		Type: "skill", Scope: document.ScopeProject, WorkspaceID: ws.ID, ProjectID: pj.ID, Status: "all",
	}, document.ListOptions{})
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	seenDeleted := false
	for _, d := range all.Items {
		if d.ID == doomed.ID {
			seenDeleted = true
		}
	}
	if !seenDeleted {
		t.Errorf("status=all should include the deleted row %s", doomed.ID)
	}

	// Count mirrors the default-list exclusion.
	n, err := docStore.Count(ctx, filter)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("Count default = %d, want 1 (live row only)", n)
	}
}

// TestDocumentList_RevivesTombstoneOnReupsert covers sty_f0b2dcc8: a
// re-upsert (e.g. `skill publish`) of a soft-deleted document must clear the
// row-level tombstone, so the revived row reappears in List/Count and sync.
// Delete flips documents.status down; Upsert must flip it back up — including
// when the re-pushed body is byte-identical (the short-circuit must not swallow
// the revive).
func TestDocumentList_RevivesTombstoneOnReupsert(t *testing.T) {
	env := testbootstrap.SetUp(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)

	docStore := document.New(env.DB)
	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	ws, err := wsStore.Create(ctx, "", "revive-ws", now)
	if err != nil {
		t.Fatalf("ws: %v", err)
	}
	pj, err := pjStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "revive-pj"}, now)
	if err != nil {
		t.Fatalf("pj: %v", err)
	}

	key := document.Key{Scope: document.ScopeProject, WorkspaceID: ws.ID, ProjectID: pj.ID, Name: "revive-skill"}
	body := "---\nname: revive-skill\n---\nbody\n"
	upsert := func(b string, at time.Time) document.Document {
		d, _, err := docStore.Upsert(ctx, document.UpsertInput{Key: key, Type: "skill", Body: b, CreatedBy: "test"}, at)
		if err != nil {
			t.Fatalf("upsert: %v", err)
		}
		return d
	}
	filter := document.ListFilter{Type: "skill", Scope: document.ScopeProject, WorkspaceID: ws.ID, ProjectID: pj.ID}
	inList := func() bool {
		res, err := docStore.List(ctx, filter, document.ListOptions{})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, d := range res.Items {
			if d.Name == "revive-skill" {
				return true
			}
		}
		return false
	}

	// Seed → delete: gone from the default list, row tombstoned.
	upsert(body, now)
	if _, _, err := docStore.Delete(ctx, key, "test", false, now.Add(time.Minute)); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if inList() {
		t.Fatalf("precondition: deleted skill still in list")
	}

	// Re-upsert with the SAME body (the body-equality short-circuit path): the
	// revive must still happen — row active, present in List, latest version active.
	revived := upsert(body, now.Add(2*time.Minute))
	if revived.Status != string(document.StatusActive) {
		t.Errorf("after revive (same body), document.Status = %q, want %q", revived.Status, document.StatusActive)
	}
	if !inList() {
		t.Errorf("revived skill (same body) absent from default list")
	}
	res, err := docStore.Get(ctx, key, document.GetOptions{})
	if err != nil {
		t.Fatalf("get after revive: %v", err)
	}
	if res.Versions[0].Status != document.StatusActive {
		t.Errorf("revived latest version status = %q, want %q", res.Versions[0].Status, document.StatusActive)
	}
	if n, err := docStore.Count(ctx, filter); err != nil || n != 1 {
		t.Errorf("Count after revive = %d (err %v), want 1", n, err)
	}

	// And a delete → re-upsert with a CHANGED body also revives (non-short-circuit path).
	if _, _, err := docStore.Delete(ctx, key, "test", false, now.Add(3*time.Minute)); err != nil {
		t.Fatalf("delete 2: %v", err)
	}
	upsert(body+"# changed\n", now.Add(4*time.Minute))
	if !inList() {
		t.Errorf("revived skill (changed body) absent from default list")
	}
}

// TestDocumentList_ScopeAll covers sty_2eccc1ea AC#2: scope:"all"
// cross-lists system + workspace + project rows in one call, with cursor
// pagination preserved. The three seed rows share a "zz-" name prefix so a
// name_contains filter isolates them from any bootstrap-seeded system rows.
func TestDocumentList_ScopeAll(t *testing.T) {
	env := testbootstrap.SetUp(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)

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

	ws, err := wsStore.Create(ctx, "", "scopeall-ws", now)
	if err != nil {
		t.Fatalf("ws: %v", err)
	}
	pj, err := pjStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "scopeall-pj"}, now)
	if err != nil {
		t.Fatalf("pj: %v", err)
	}

	seed := func(key document.Key, when time.Time) document.Document {
		d, _, err := docStore.Upsert(ctx, document.UpsertInput{
			Key: key, Type: "document", Body: "b", CreatedBy: "test", SystemWriteAllowed: true,
		}, when)
		if err != nil {
			t.Fatalf("seed %s: %v", key.Name, err)
		}
		return d
	}
	sysDoc := seed(document.Key{Scope: document.ScopeSystem, Name: "zz-sys"}, now.Add(1*time.Minute))
	wsDoc := seed(document.Key{Scope: document.ScopeWorkspace, WorkspaceID: ws.ID, Name: "zz-ws"}, now.Add(2*time.Minute))
	pjDoc := seed(document.Key{Scope: document.ScopeProject, WorkspaceID: ws.ID, ProjectID: pj.ID, Name: "zz-proj"}, now.Add(3*time.Minute))

	list := func(t *testing.T, req verb.DocumentListRequest) verb.DocumentListResponse {
		t.Helper()
		body, _ := json.Marshal(req)
		raw, err := verb.Dispatch(ctx, "document_list", body)
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		var resp verb.DocumentListResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return resp
	}

	t.Run("scope=all crosses system+workspace+project", func(t *testing.T) {
		resp := list(t, verb.DocumentListRequest{
			Type: "document", Scope: "all", WorkspaceID: ws.ID, ProjectID: pj.ID, NameContains: "zz-",
		})
		assertIDs(t, resp.Items, []string{sysDoc.ID, wsDoc.ID, pjDoc.ID})
	})

	t.Run("scope=project sees only the project row", func(t *testing.T) {
		resp := list(t, verb.DocumentListRequest{
			Type: "document", Scope: "project", WorkspaceID: ws.ID, ProjectID: pj.ID, NameContains: "zz-",
		})
		assertIDs(t, resp.Items, []string{pjDoc.ID})
	})

	t.Run("scope=all requires workspace_id", func(t *testing.T) {
		body, _ := json.Marshal(verb.DocumentListRequest{Type: "document", Scope: "all", NameContains: "zz-"})
		if _, err := verb.Dispatch(ctx, "document_list", body); err == nil {
			t.Fatalf("scope=all without workspace_id should error")
		}
	})

	t.Run("scope=all cursor pagination yields each row exactly once", func(t *testing.T) {
		seen := map[string]bool{}
		cursor := ""
		for i := 0; i < 10; i++ {
			resp := list(t, verb.DocumentListRequest{
				Type: "document", Scope: "all", WorkspaceID: ws.ID, ProjectID: pj.ID,
				NameContains: "zz-", Limit: 1, Cursor: cursor,
			})
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
		for _, id := range []string{sysDoc.ID, wsDoc.ID, pjDoc.ID} {
			if !seen[id] {
				t.Errorf("scope=all pagination missed %s", id)
			}
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
