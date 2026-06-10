//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestReviewSkillCascadeOverride pins sty_f302bd8b AC4/AC6: a per-type review
// skill ships as a system-scope baseline (satellites-document-review /
// satellites-principle-review / satellites-skill-review), and a project OVERRIDES
// it by authoring a project-scope skill
// of the same name — config-over-code, the existing inherit cascade
// (user > project > workspace > system). The override is "what runs for the
// project"; removing it restores the inherited system baseline. No new
// resolution code — this asserts the standard document_get inherit path for
// the review-skill name.
func TestReviewSkillCascadeOverride(t *testing.T) {
	env := testbootstrap.SetUp(t)
	ctx := context.Background()
	now := time.Now().UTC()

	docStore := document.New(env.DB)
	authStore := auth.New(env.DB)
	wsStore := workspace.New(env.DB)
	verb.SetDocumentStore(docStore)
	verb.SetAuthStore(authStore)
	verb.SetWorkspaceStore(wsStore)
	t.Cleanup(func() {
		verb.SetDocumentStore(nil)
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
	})

	if _, err := env.DB.Exec(`TRUNCATE api_keys, users, workspace_members RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if err := authStore.DevSeed(ctx); err != nil {
		t.Fatalf("dev seed: %v", err)
	}
	admin, err := authStore.GetUserByEmail(ctx, auth.DevAdminEmail)
	if err != nil {
		t.Fatalf("admin: %v", err)
	}
	ctxAdmin := authWithUser(ctx, admin)

	ws, err := wsStore.Create(ctx, admin.ID, "RW", now)
	if err != nil {
		t.Fatalf("ws: %v", err)
	}
	if err := wsStore.AddMember(ctx, ws.ID, admin.ID, workspace.RoleAdmin, admin.ID, now); err != nil {
		t.Fatalf("member: %v", err)
	}
	if _, err := env.DB.Exec(`INSERT INTO projects (id, workspace_id, name) VALUES ($1,$2,'p')`, "proj_review", ws.ID); err != nil {
		t.Fatalf("project: %v", err)
	}

	// Global baseline review skill at system scope + a project override of the
	// same name (type:skill, as the real review skills are seeded).
	if err := document.SeedSystemTyped(ctx, docStore, document.TypeSkill, "satellites-document-review", "system baseline review", "system:seed", now); err != nil {
		t.Fatalf("seed system skill: %v", err)
	}
	projKey := document.Key{Scope: document.ScopeProject, WorkspaceID: ws.ID, ProjectID: "proj_review", Name: "satellites-document-review"}
	if _, _, err := docStore.Upsert(ctx, document.UpsertInput{Key: projKey, Type: document.TypeSkill, Body: "project override review"}, now); err != nil {
		t.Fatalf("upsert project skill: %v", err)
	}

	get := func(t *testing.T) verb.DocumentGetResponse {
		t.Helper()
		raw, err := verb.Dispatch(ctxAdmin, "document_get", json.RawMessage(
			`{"name":"satellites-document-review","scope":"project","workspace_id":"`+ws.ID+`","project_id":"proj_review","inherit":true}`))
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		var got verb.DocumentGetResponse
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	t.Run("project override wins", func(t *testing.T) {
		got := get(t)
		if got.ResolvedScope != "project" {
			t.Fatalf("resolved_scope=%s want project", got.ResolvedScope)
		}
		if got.Versions[0].Body != "project override review" {
			t.Fatalf("body=%q want project override", got.Versions[0].Body)
		}
	})

	t.Run("removing the override inherits the system baseline", func(t *testing.T) {
		if _, _, err := docStore.Delete(ctx, projKey, admin.ID, false, now); err != nil {
			t.Fatalf("delete override: %v", err)
		}
		got := get(t)
		if got.ResolvedScope != "system" {
			t.Fatalf("resolved_scope=%s want system (inherited baseline)", got.ResolvedScope)
		}
		if got.Versions[0].Body != "system baseline review" {
			t.Fatalf("body=%q want system baseline", got.Versions[0].Body)
		}
	})
}
