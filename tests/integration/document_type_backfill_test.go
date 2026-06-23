//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// backfillUpdateSQL mirrors 0046_backfill_document_type_tag.up.sql — the test runs
// the identical predicate against seeded rows to lock which documents it tags.
const backfillUpdateSQL = `
UPDATE documents
SET tags = array_append(tags, 'type:document')
WHERE type = 'document'
  AND scope IN ('workspace', 'project')
  AND status = 'active'
  AND NOT EXISTS (
    SELECT 1 FROM unnest(tags) AS t
    WHERE t LIKE 'type:%' OR t LIKE 'principles:%' OR t LIKE 'kind:%'
  )`

// TestDocumentTypeBackfill pins sty_0ca51ef4: the backfill tags plain/uploaded
// user documents type:document while leaving substrate (principles:*, kind:*) and
// already-classified (type:*) rows untouched — and never duplicates the tag.
func TestDocumentTypeBackfill(t *testing.T) {
	env := testbootstrap.SetUp(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)

	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	docStore := document.New(env.DB)

	ws, err := wsStore.Create(ctx, "", "backfill-ws", now)
	if err != nil {
		t.Fatalf("ws: %v", err)
	}
	pj, err := pjStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "backfill-pj"}, now)
	if err != nil {
		t.Fatalf("pj: %v", err)
	}

	seed := func(name string, tags []string) string {
		t.Helper()
		doc, _, err := docStore.Upsert(ctx, document.UpsertInput{
			Key:       document.Key{Scope: document.ScopeProject, WorkspaceID: ws.ID, ProjectID: pj.ID, Name: name},
			Type:      document.TypeDocument,
			Body:      "# " + name,
			CreatedBy: "system:test",
		}, now)
		if err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
		if len(tags) > 0 {
			if _, err := docStore.SetDocumentTags(ctx, doc.ID, tags, now); err != nil {
				t.Fatalf("tag %s: %v", name, err)
			}
		}
		return doc.ID
	}

	plain := seed("uploaded-doc", nil)                          // → gains type:document
	principle := seed("a-principle", []string{"principles:project"}) // untouched
	reference := seed("a-reference", []string{"kind:reference"})      // untouched
	already := seed("agent-output", []string{"type:document", "phase:discovery"}) // unchanged, no dup

	if _, err := env.DB.ExecContext(ctx, backfillUpdateSQL); err != nil {
		t.Fatalf("run backfill: %v", err)
	}

	tagsOf := func(id string) []string {
		t.Helper()
		d, err := docStore.GetByID(ctx, id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		return d.Tags
	}

	if !sortedEqual(tagsOf(plain), []string{"type:document"}) {
		t.Errorf("plain doc tags = %v, want [type:document]", tagsOf(plain))
	}
	if !sortedEqual(tagsOf(principle), []string{"principles:project"}) {
		t.Errorf("principle tags = %v, want unchanged", tagsOf(principle))
	}
	if !sortedEqual(tagsOf(reference), []string{"kind:reference"}) {
		t.Errorf("reference tags = %v, want unchanged", tagsOf(reference))
	}
	if !sortedEqual(tagsOf(already), []string{"type:document", "phase:discovery"}) {
		t.Errorf("already-typed tags = %v, want unchanged (no duplicate)", tagsOf(already))
	}
}
