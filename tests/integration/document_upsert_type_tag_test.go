//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestDocumentUpsertDerivesTypeTag pins sty_5ceec3f1: a type:document row created
// via the document_upsert verb (the MCP/CLI write path) must carry the
// type:document TAG, so it is returned by the documents panel's list-filter shape
// (Tags:["type:document"]) — not just by a type-column query. Before the fix the
// verb set tags to exactly the caller's set, so a column-only "document" was
// invisible in the panel (0 / 0).
func TestDocumentUpsertDerivesTypeTag(t *testing.T) {
	env := testbootstrap.SetUp(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)

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

	ws, err := wsStore.Create(ctx, "", "type-tag-ws", now)
	if err != nil {
		t.Fatalf("ws: %v", err)
	}
	pj, err := pjStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "type-tag-pj"}, now)
	if err != nil {
		t.Fatalf("pj: %v", err)
	}

	upsert := func(name string, req map[string]any) document.Document {
		t.Helper()
		req["type"] = "document"
		req["scope"] = "project"
		req["workspace_id"] = ws.ID
		req["project_id"] = pj.ID
		req["name"] = name
		raw, derr := verb.Dispatch(ctx, "document_upsert", mustJSON(t, req))
		if derr != nil {
			t.Fatalf("upsert %s: %v", name, derr)
		}
		var resp verb.DocumentUpsertResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("decode upsert %s: %v", name, err)
		}
		return resp.Document
	}

	// (a) no tags at all → the verb must still derive type:document.
	noTags := upsert("no-tags-doc", map[string]any{"body": "# A"})
	if !slices.Contains(noTags.Tags, "type:document") {
		t.Errorf("upsert with no tags: want type:document derived, got %v", noTags.Tags)
	}
	// (b) caller-supplied non-type tags → type:document is added alongside them.
	withOther := upsert("other-tags-doc", map[string]any{"body": "# B", "tags": []string{"area:docs"}})
	if !slices.Contains(withOther.Tags, "type:document") || !slices.Contains(withOther.Tags, "area:docs") {
		t.Errorf("upsert with other tags: want both type:document and area:docs, got %v", withOther.Tags)
	}
	// (c) a descriptive kind:* facet must NOT suppress the derive (sty_5711ab3e):
	// a kind:reference document IS a project document and belongs in the panel.
	withKind := upsert("kind-reference-doc", map[string]any{"body": "# C", "tags": []string{"discovery", "kind:reference"}})
	if !slices.Contains(withKind.Tags, "type:document") || !slices.Contains(withKind.Tags, "kind:reference") {
		t.Errorf("upsert with kind:reference: want both type:document and kind:reference, got %v", withKind.Tags)
	}
	// (d) an explicit type:* classification is still respected — no type:document stamp.
	withType := upsert("typed-diagram-doc", map[string]any{"body": "# D", "tags": []string{"type:diagram"}})
	if slices.Contains(withType.Tags, "type:document") {
		t.Errorf("upsert with type:diagram: want type:document NOT derived, got %v", withType.Tags)
	}

	// The panel's list-filter shape (Tags:["type:document"]) must now return both.
	listReq, _ := json.Marshal(verb.DocumentListRequest{
		Scope: "project", WorkspaceID: ws.ID, ProjectID: pj.ID,
		Type: "document", Tags: []string{"type:document"}, Limit: 200,
	})
	listRaw, err := verb.Dispatch(ctx, "document_list", listReq)
	if err != nil {
		t.Fatalf("document_list: %v", err)
	}
	var list verb.DocumentListResponse
	if err := json.Unmarshal(listRaw, &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	got := map[string]bool{}
	for _, d := range list.Items {
		got[d.Name] = true
	}
	if !got["no-tags-doc"] || !got["other-tags-doc"] || !got["kind-reference-doc"] {
		names := make([]string, 0, len(list.Items))
		for _, d := range list.Items {
			names = append(names, d.Name)
		}
		t.Fatalf("panel filter Tags:[type:document] missing upsert-created docs; got %v", names)
	}
	if got["typed-diagram-doc"] {
		t.Errorf("panel filter Tags:[type:document] unexpectedly returned the type:diagram doc")
	}
}
