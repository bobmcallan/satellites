//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"sort"
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

// TestMCPWriteSurface_EndToEnd drives the four unified document_* verbs
// over the real MCP streamable-HTTP transport. Story create / update /
// get / delete are exercised through document_upsert (type:"story") and
// document_get / document_delete (by id) — there is no story_* surface
// post-unification.
//
// Tag round-trip on document_upsert id-mode covers AC #4 from the
// original write-surface story (add / remove / clear / keep).
func TestMCPWriteSurface_EndToEnd(t *testing.T) {
	env := testbootstrap.SetUp(t)
	ctx := context.Background()
	now := time.Now().UTC()

	wsStore := workspace.New(env.DB)
	ws, err := wsStore.Create(ctx, "", "test-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	pjStore := project.New(env.DB)
	pj, err := pjStore.Create(ctx, project.CreateInput{
		WorkspaceID: ws.ID,
		Name:        "mcp-write-surface-test",
	}, now)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	docStore := document.New(env.DB)
	verb.SetProjectStore(pjStore)
	verb.SetLedgerStore(ledger.New(env.DB))
	verb.SetDocumentStore(docStore)
	t.Cleanup(func() {
		verb.SetProjectStore(nil)
		verb.SetLedgerStore(nil)
		verb.SetDocumentStore(nil)
	})

	ts := httptest.NewServer(mcpserver.HTTPHandler(mcpserver.New()))
	t.Cleanup(ts.Close)

	cli, err := client.NewStreamableHttpClient(ts.URL)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })

	if err := cli.Start(ctx); err != nil {
		t.Fatalf("client.Start: %v", err)
	}
	if _, err := cli.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo:      mcp.Implementation{Name: "mcp-write-surface-test", Version: "1"},
		},
	}); err != nil {
		t.Fatalf("client.Initialize: %v", err)
	}

	call := func(t *testing.T, name string, args map[string]any) string {
		t.Helper()
		req := mcp.CallToolRequest{}
		req.Params.Name = name
		req.Params.Arguments = args
		res, err := cli.CallTool(ctx, req)
		if err != nil {
			t.Fatalf("CallTool %s: %v", name, err)
		}
		if res.IsError {
			t.Fatalf("CallTool %s returned IsError; content=%+v", name, res.Content)
		}
		if len(res.Content) == 0 {
			t.Fatalf("CallTool %s returned empty content", name)
		}
		tc, ok := res.Content[0].(mcp.TextContent)
		if !ok {
			t.Fatalf("CallTool %s: content[0] is %T, want TextContent", name, res.Content[0])
		}
		return tc.Text
	}

	// ---- story create via document_upsert (type:story) ----
	var created document.Document
	t.Run("story_create_with_tags", func(t *testing.T) {
		body := call(t, "document_upsert", map[string]any{
			"type":       "story",
			"project_id": pj.ID,
			"name":       "MCP write surface E2E",
			"body":       "exercises the streamable-HTTP transport",
			"category":   "test",
			"priority":   "medium",
			"status":     "backlog",
			"tags":       []string{"area:mcp", "scope:e2e"},
		})
		var resp verb.DocumentUpsertResponse
		if err := json.Unmarshal([]byte(body), &resp); err != nil {
			t.Fatalf("decode: %v\n%s", err, body)
		}
		created = resp.Document
		if created.ID == "" {
			t.Fatal("document_upsert returned empty id")
		}
		if created.Type != "story" {
			t.Errorf("type: got %q want story", created.Type)
		}
		assertTagsEqual(t, created.Tags, []string{"area:mcp", "scope:e2e"})
	})

	// ---- story get via document_get (id-addressed) ----
	t.Run("story_get_by_id", func(t *testing.T) {
		body := call(t, "document_get", map[string]any{"id": created.ID})
		var resp verb.DocumentGetResponse
		if err := json.Unmarshal([]byte(body), &resp); err != nil {
			t.Fatalf("decode: %v\n%s", err, body)
		}
		if resp.Document.ID != created.ID || resp.Document.Name != created.Name {
			t.Errorf("mismatch: got id=%s name=%q want id=%s name=%q",
				resp.Document.ID, resp.Document.Name, created.ID, created.Name)
		}
	})

	// ---- story update via document_upsert (id-addressed) — tag round-trip ----
	t.Run("story_update_tag_addition", func(t *testing.T) {
		body := call(t, "document_upsert", map[string]any{
			"id":   created.ID,
			"tags": []string{"area:mcp", "scope:e2e", "epic:mcp-write-surface"},
		})
		var resp verb.DocumentUpsertResponse
		if err := json.Unmarshal([]byte(body), &resp); err != nil {
			t.Fatal(err)
		}
		assertTagsEqual(t, resp.Document.Tags, []string{"area:mcp", "scope:e2e", "epic:mcp-write-surface"})
	})
	t.Run("story_update_tag_removal", func(t *testing.T) {
		body := call(t, "document_upsert", map[string]any{
			"id":   created.ID,
			"tags": []string{"scope:e2e"},
		})
		var resp verb.DocumentUpsertResponse
		if err := json.Unmarshal([]byte(body), &resp); err != nil {
			t.Fatal(err)
		}
		assertTagsEqual(t, resp.Document.Tags, []string{"scope:e2e"})
	})
	t.Run("story_update_tag_clear", func(t *testing.T) {
		body := call(t, "document_upsert", map[string]any{
			"id":   created.ID,
			"tags": []string{},
		})
		var resp verb.DocumentUpsertResponse
		if err := json.Unmarshal([]byte(body), &resp); err != nil {
			t.Fatal(err)
		}
		assertTagsEqual(t, resp.Document.Tags, []string{})
	})
	t.Run("story_update_tag_omit_keeps_existing", func(t *testing.T) {
		_ = call(t, "document_upsert", map[string]any{
			"id":   created.ID,
			"tags": []string{"keep:me"},
		})
		// Update title (name) only — tags must be unchanged.
		body := call(t, "document_upsert", map[string]any{
			"id":   created.ID,
			"name": "title-only-edit",
		})
		var resp verb.DocumentUpsertResponse
		if err := json.Unmarshal([]byte(body), &resp); err != nil {
			t.Fatal(err)
		}
		if resp.Document.Name != "title-only-edit" {
			t.Errorf("name not updated: got %q", resp.Document.Name)
		}
		assertTagsEqual(t, resp.Document.Tags, []string{"keep:me"})
	})

	// ---- type:document content upsert SUCCEEDS over MCP (sty_06b8e38b) ----
	// The MCP write surface is {document, story}: a plain corpus document is
	// writable (still scope-bounded by membership). The story sub-tests above cover
	// story writes; a plain document has no reviewer to bypass.
	t.Run("mcp_allows_document_content_upsert", func(t *testing.T) {
		req := mcp.CallToolRequest{}
		req.Params.Name = "document_upsert"
		req.Params.Arguments = map[string]any{
			"type": "document", "name": "corpus-note", "scope": "project",
			"workspace_id": ws.ID, "project_id": pj.ID, "body": "# Note\n\nbody",
		}
		res, err := cli.CallTool(ctx, req)
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
		if res.IsError {
			t.Fatalf("MCP document content upsert must succeed; got error %+v", res.Content)
		}
	})

	// ---- types OFF the MCP write surface are REFUSED (sty_06b8e38b) ----
	// task is CLI-only; skill/principle/workflow carry a reviewer + attestation.
	for _, typ := range []string{"task", "skill", "principle", "workflow"} {
		typ := typ
		t.Run("mcp_refuses_"+typ+"_content_upsert", func(t *testing.T) {
			req := mcp.CallToolRequest{}
			req.Params.Name = "document_upsert"
			req.Params.Arguments = map[string]any{
				"type": typ, "name": "blocked-" + typ, "scope": "project",
				"workspace_id": ws.ID, "project_id": pj.ID, "body": "x",
			}
			res, err := cli.CallTool(ctx, req)
			if err != nil {
				t.Fatalf("CallTool: %v", err)
			}
			if !res.IsError {
				t.Errorf("MCP %s content upsert must be refused; got %+v", typ, res.Content)
			}
		})
	}

	// ---- a behaviour write smuggled as type:document is REFUSED (sty_06b8e38b) ----
	// A kind:workflow / principles:* tag triggers the review-attestation barrier even
	// on a type:document row, so opening type:document opens no side door for
	// review-gated substrate.
	for _, tc := range []struct{ name, tag string }{
		{"workflow_by_tag", "kind:workflow"},
		{"principle_by_tag", "principles:always"},
	} {
		tc := tc
		t.Run("mcp_refuses_document_with_"+tc.name, func(t *testing.T) {
			req := mcp.CallToolRequest{}
			req.Params.Name = "document_upsert"
			req.Params.Arguments = map[string]any{
				"type": "document", "name": "smuggle-" + tc.name, "scope": "project",
				"workspace_id": ws.ID, "project_id": pj.ID, "body": "x",
				"tags": []string{tc.tag},
			}
			res, err := cli.CallTool(ctx, req)
			if err != nil {
				t.Fatalf("CallTool: %v", err)
			}
			if !res.IsError {
				t.Errorf("MCP document+%s must be refused (attestation barrier); got %+v", tc.tag, res.Content)
			}
		})
	}

	// ---- skill delete is REFUSED over MCP (sty_1c234792) ----
	// MCP can neither read nor write a skill; the delete side door must be
	// closed too, or an MCP caller can undo review-gated uploads. The skill
	// row is seeded directly through the store (as the CLI upload path would).
	t.Run("mcp_refuses_skill_delete", func(t *testing.T) {
		seeded, _, err := docStore.Upsert(ctx, document.UpsertInput{
			Key:  document.Key{Scope: document.ScopeProject, WorkspaceID: ws.ID, ProjectID: pj.ID, Name: "delete-door-skill"},
			Type: document.TypeSkill,
			Body: "---\nname: delete-door-skill\nkind: gate\n---\n# g\n",
		}, time.Now().UTC())
		if err != nil {
			t.Fatalf("seed skill: %v", err)
		}
		for name, args := range map[string]map[string]any{
			"by-id":   {"id": seeded.ID},
			"by-name": {"name": "delete-door-skill", "scope": "project", "workspace_id": ws.ID, "project_id": pj.ID},
		} {
			req := mcp.CallToolRequest{}
			req.Params.Name = "document_delete"
			req.Params.Arguments = args
			res, err := cli.CallTool(ctx, req)
			if err != nil {
				t.Fatalf("CallTool %s: %v", name, err)
			}
			if !res.IsError {
				t.Errorf("MCP skill delete (%s) must be refused; got %+v", name, res.Content)
			}
		}
		if d, err := docStore.GetByID(ctx, seeded.ID); err != nil || d.Status == string(document.StatusDeleted) {
			t.Errorf("skill must survive the refused deletes: err=%v status=%v", err, d.Status)
		}
	})

	// ---- story delete via document_delete (id-addressed) ----
	t.Run("story_delete_by_id", func(t *testing.T) {
		_ = call(t, "document_delete", map[string]any{"id": created.ID})

		req := mcp.CallToolRequest{}
		req.Params.Name = "document_get"
		req.Params.Arguments = map[string]any{"id": created.ID}
		res, err := cli.CallTool(ctx, req)
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
		if !res.IsError {
			t.Errorf("document_get after hard-delete should error; got %+v", res.Content)
		}
	})
}

func assertTagsEqual(t *testing.T, got, want []string) {
	t.Helper()
	g := append([]string(nil), got...)
	w := append([]string(nil), want...)
	sort.Strings(g)
	sort.Strings(w)
	if !reflect.DeepEqual(g, w) {
		t.Errorf("tags: got %v want %v", g, w)
	}
}
