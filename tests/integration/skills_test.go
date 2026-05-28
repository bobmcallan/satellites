//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestSystemSeedReconcile_TypeSkill verifies the type-aware reconciler:
// a TypeSkill seed materialises as a documents row with type='skill',
// re-applies are idempotent, and a body change appends a new version
// row (version chain continuity).
func TestSystemSeedReconcile_TypeSkill(t *testing.T) {
	env := testbootstrap.SetUp(t)
	testbootstrap.Reset(t, env)

	docs := document.New(env.DB)
	sys := document.NewSystemSeedStore(env.DB)
	ctx := context.Background()
	name := "test-reviewer-skill"
	body := "# reviewer skill\n\nminimal rubric body.\n"
	now := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)

	res, err := document.ReconcileSystemSeedTyped(ctx, sys, docs, document.TypeSkill, name, body, nil, "system:seed", now)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !res.Created || !res.Changed {
		t.Fatalf("expected Created=true Changed=true, got %+v", res)
	}

	got, err := docs.Get(ctx, document.Key{Scope: document.ScopeSystem, Name: name}, document.GetOptions{AllVersions: true})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Document.Type != document.TypeSkill {
		t.Fatalf("type = %q, want %q", got.Document.Type, document.TypeSkill)
	}
	if len(got.Versions) != 1 {
		t.Fatalf("expected one version on first apply, got %d", len(got.Versions))
	}

	// Body change appends a new version; type stays 'skill'.
	body2 := body + "\nupdated rubric line.\n"
	res2, err := document.ReconcileSystemSeedTyped(ctx, sys, docs, document.TypeSkill, name, body2, nil, "system:seed", now.Add(time.Hour))
	if err != nil {
		t.Fatalf("reconcile v2: %v", err)
	}
	if !res2.Changed || res2.Created {
		t.Fatalf("expected Changed=true Created=false, got %+v", res2)
	}
	got2, err := docs.Get(ctx, document.Key{Scope: document.ScopeSystem, Name: name}, document.GetOptions{AllVersions: true})
	if err != nil {
		t.Fatalf("get v2: %v", err)
	}
	if got2.Document.Type != document.TypeSkill {
		t.Fatalf("type drifted to %q after re-apply", got2.Document.Type)
	}
	if len(got2.Versions) != 2 {
		t.Fatalf("expected two versions after body change, got %d", len(got2.Versions))
	}
}

// TestDocumentList_TypeSkillFilter verifies the existing document_list
// verb's type filter admits 'skill' and returns only skill rows. No
// MCP-surface additions — the verb is unchanged; only the type filter
// value is new.
func TestDocumentList_TypeSkillFilter(t *testing.T) {
	env := testbootstrap.SetUp(t)
	testbootstrap.Reset(t, env)

	docs := document.New(env.DB)
	verb.SetDocumentStore(docs)
	t.Cleanup(func() { verb.SetDocumentStore(nil) })

	ctx := context.Background()
	now := time.Date(2026, 5, 28, 11, 0, 0, 0, time.UTC)

	// Seed one skill and one plain document at system scope so the
	// filter has something to discriminate against.
	if err := document.SeedSystemTyped(ctx, docs, document.TypeSkill, "list-skill-one", "skill body 1", "system:seed", now); err != nil {
		t.Fatalf("seed skill: %v", err)
	}
	if err := document.SeedSystem(ctx, docs, "list-doc-one", "doc body 1", "system:seed", now); err != nil {
		t.Fatalf("seed doc: %v", err)
	}

	rawSkills, err := verb.Dispatch(ctx, "document_list",
		json.RawMessage(`{"type":"skill","scope":"system"}`))
	if err != nil {
		t.Fatalf("dispatch skill list: %v", err)
	}
	var skillRes verb.DocumentListResponse
	if err := json.Unmarshal(rawSkills, &skillRes); err != nil {
		t.Fatal(err)
	}
	if len(skillRes.Items) != 1 || skillRes.Items[0].Name != "list-skill-one" || skillRes.Items[0].Type != document.TypeSkill {
		t.Fatalf("expected exactly one skill named list-skill-one, got %+v", skillRes.Items)
	}

	rawDocs, err := verb.Dispatch(ctx, "document_list",
		json.RawMessage(`{"type":"document","scope":"system"}`))
	if err != nil {
		t.Fatalf("dispatch doc list: %v", err)
	}
	var docRes verb.DocumentListResponse
	if err := json.Unmarshal(rawDocs, &docRes); err != nil {
		t.Fatal(err)
	}
	for _, it := range docRes.Items {
		if it.Type == document.TypeSkill {
			t.Errorf("type=document filter returned a skill: %+v", it)
		}
	}
}

// TestDocumentGet_SkillByKey verifies document_get resolves a skill by
// (scope, name) without a type predicate — the response surfaces the
// stored type so callers can branch on it.
func TestDocumentGet_SkillByKey(t *testing.T) {
	env := testbootstrap.SetUp(t)
	testbootstrap.Reset(t, env)

	docs := document.New(env.DB)
	verb.SetDocumentStore(docs)
	t.Cleanup(func() { verb.SetDocumentStore(nil) })

	ctx := context.Background()
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	if err := document.SeedSystemTyped(ctx, docs, document.TypeSkill, "get-skill-one", "skill body", "system:seed", now); err != nil {
		t.Fatalf("seed: %v", err)
	}

	raw, err := verb.Dispatch(ctx, "document_get",
		json.RawMessage(`{"name":"get-skill-one","scope":"system"}`))
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	var got verb.DocumentGetResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Document.Type != document.TypeSkill {
		t.Fatalf("response type = %q, want %q", got.Document.Type, document.TypeSkill)
	}
	if got.Document.Name != "get-skill-one" {
		t.Fatalf("response name = %q, want %q", got.Document.Name, "get-skill-one")
	}
}
