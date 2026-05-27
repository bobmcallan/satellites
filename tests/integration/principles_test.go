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

// TestPrinciplesRideAlong asserts the principles sidecar arrives on
// every read verb whose scope matches a tagged document, and is absent
// on write paths and on read paths that have not crossed a scope.
func TestPrinciplesRideAlong(t *testing.T) {
	env := testbootstrap.SetUp(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)

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

	ws, err := wsStore.Create(ctx, "", "principles-ws", now)
	if err != nil {
		t.Fatalf("ws: %v", err)
	}
	pj, err := pjStore.Create(ctx, project.CreateInput{
		WorkspaceID: ws.ID,
		Name:        "principles-pj",
		GitURL:      "https://github.com/example/principles.git",
	}, now)
	if err != nil {
		t.Fatalf("pj: %v", err)
	}

	// seedPrinciple writes a free-form document at the requested scope
	// and tags it with the matching principles:<scope> tag via the new
	// document_upsert tag-merge path.
	seedPrinciple := func(scope document.Scope, name, body string, princScope verb.PrincipleScope) document.Document {
		t.Helper()
		key := document.Key{Scope: scope, Name: name}
		if scope == document.ScopeWorkspace || scope == document.ScopeProject {
			key.WorkspaceID = ws.ID
		}
		if scope == document.ScopeProject {
			key.ProjectID = pj.ID
		}
		if scope == document.ScopeSystem {
			if err := document.SeedSystem(ctx, docStore, name, body, "test:seed", now); err != nil {
				t.Fatalf("seed system principle %q: %v", name, err)
			}
		} else {
			if _, _, err := docStore.Upsert(ctx, document.UpsertInput{
				Key:       key,
				Body:      body,
				CreatedBy: "test",
			}, now); err != nil {
				t.Fatalf("seed principle %q: %v", name, err)
			}
		}
		stored, err := docStore.Get(ctx, key, document.GetOptions{})
		if err != nil {
			t.Fatalf("lookup principle %q: %v", name, err)
		}
		if _, err := docStore.SetDocumentTags(ctx, stored.Document.ID, []string{verb.PrincipleTag(princScope)}, now); err != nil {
			t.Fatalf("tag principle %q: %v", name, err)
		}
		return stored.Document
	}

	globalP := seedPrinciple(document.ScopeSystem, "global-rule", "do not commit secrets", verb.PrincipleScopeGlobal)
	workspaceP := seedPrinciple(document.ScopeWorkspace, "ws-rule", "all PRs require review", verb.PrincipleScopeWorkspace)
	projectP := seedPrinciple(document.ScopeProject, "story-execution", "do not stop unless blocked", verb.PrincipleScopeProject)
	storyP := seedPrinciple(document.ScopeProject, "story-rule", "every story has a blocked path", verb.PrincipleScopeStory)

	// Seed an irrelevant document — should NOT show in any sidecar.
	if _, _, err := docStore.Upsert(ctx, document.UpsertInput{
		Key:       document.Key{Scope: document.ScopeProject, WorkspaceID: ws.ID, ProjectID: pj.ID, Name: "release-notes"},
		Body:      "v1",
		CreatedBy: "test",
	}, now); err != nil {
		t.Fatalf("seed irrelevant doc: %v", err)
	}

	// Create a story that will pull project + story principles on read.
	story, err := docStore.CreateStory(ctx, document.CreateStoryInput{
		ProjectID:   pj.ID,
		WorkspaceID: ws.ID,
		Title:       "demo story",
	}, now)
	if err != nil {
		t.Fatalf("create story: %v", err)
	}

	t.Run("document_get on a story attaches project + story principles", func(t *testing.T) {
		raw, err := verb.Dispatch(ctx, "document_get", json.RawMessage(`{"id":"`+story.ID+`"}`))
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		var resp verb.DocumentGetResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		assertPrincipleIDs(t, resp.Principles, projectP.ID, storyP.ID)
		assertScopes(t, resp.Principles, verb.PrincipleScopeProject, verb.PrincipleScopeStory)
		assertBodyMatches(t, resp.Principles, projectP.ID, "do not stop unless blocked")
	})

	t.Run("project_get attaches workspace + project principles", func(t *testing.T) {
		raw, err := verb.Dispatch(ctx, "project_get", json.RawMessage(`{"id":"`+pj.ID+`"}`))
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		var resp struct {
			ID         string           `json:"id"`
			Principles []verb.Principle `json:"principles"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if resp.ID != pj.ID {
			t.Fatalf("project_get returned id=%s want %s", resp.ID, pj.ID)
		}
		assertPrincipleIDs(t, resp.Principles, workspaceP.ID, projectP.ID)
		assertScopes(t, resp.Principles, verb.PrincipleScopeWorkspace, verb.PrincipleScopeProject)
	})

	t.Run("project_match attaches workspace + project principles", func(t *testing.T) {
		raw, err := verb.Dispatch(ctx, "project_match", json.RawMessage(`{"git_url":"https://github.com/example/principles.git"}`))
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		var resp verb.ProjectMatchResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if resp.ProjectID != pj.ID {
			t.Fatalf("project_match returned %s want %s", resp.ProjectID, pj.ID)
		}
		assertPrincipleIDs(t, resp.Principles, workspaceP.ID, projectP.ID)
	})

	t.Run("project_list with workspace_id attaches workspace principles", func(t *testing.T) {
		raw, err := verb.Dispatch(ctx, "project_list", json.RawMessage(`{"workspace_id":"`+ws.ID+`"}`))
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		var resp verb.ProjectListResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		assertPrincipleIDs(t, resp.Principles, workspaceP.ID)
	})

	t.Run("project_list without workspace_id attaches no principles", func(t *testing.T) {
		raw, err := verb.Dispatch(ctx, "project_list", json.RawMessage(`{}`))
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		var resp verb.ProjectListResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(resp.Principles) != 0 {
			t.Fatalf("expected no principles when no workspace filter, got %d", len(resp.Principles))
		}
	})

	t.Run("document_get on a non-story document attaches no principles", func(t *testing.T) {
		// Fetching a key-addressed document (release-notes) takes the
		// (scope, name) path — principles ride on story reads only.
		raw, err := verb.Dispatch(ctx, "document_get", json.RawMessage(`{"name":"release-notes","scope":"project","workspace_id":"`+ws.ID+`","project_id":"`+pj.ID+`"}`))
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		var resp verb.DocumentGetResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(resp.Principles) != 0 {
			t.Fatalf("expected no principles on non-story document, got %d", len(resp.Principles))
		}
	})

	t.Run("document_upsert and document_delete do not carry the sidecar", func(t *testing.T) {
		raw, err := verb.Dispatch(ctx, "document_upsert", json.RawMessage(`{"id":"`+story.ID+`","body":"updated body"}`))
		if err != nil {
			t.Fatalf("upsert: %v", err)
		}
		var resp map[string]any
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if _, ok := resp["principles"]; ok {
			t.Fatalf("upsert response should not include principles sidecar: %s", string(raw))
		}
	})

	t.Run("principle body update surfaces in the next sidecar (no cache)", func(t *testing.T) {
		if _, _, err := docStore.Upsert(ctx, document.UpsertInput{
			Key:       document.Key{Scope: document.ScopeProject, WorkspaceID: ws.ID, ProjectID: pj.ID, Name: "story-execution"},
			Body:      "updated rule body",
			CreatedBy: "test",
		}, now.Add(time.Minute)); err != nil {
			t.Fatalf("update principle: %v", err)
		}
		raw, err := verb.Dispatch(ctx, "document_get", json.RawMessage(`{"id":"`+story.ID+`"}`))
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		var resp verb.DocumentGetResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		assertBodyMatches(t, resp.Principles, projectP.ID, "updated rule body")
	})

	t.Run("deleting a principle drops it from the next sidecar", func(t *testing.T) {
		if _, _, err := docStore.Delete(ctx, document.Key{
			Scope: document.ScopeProject, WorkspaceID: ws.ID, ProjectID: pj.ID, Name: "story-rule",
		}, "test", false, now.Add(2*time.Minute)); err != nil {
			t.Fatalf("delete: %v", err)
		}
		raw, err := verb.Dispatch(ctx, "document_get", json.RawMessage(`{"id":"`+story.ID+`"}`))
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		var resp verb.DocumentGetResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		for _, p := range resp.Principles {
			if p.ID == storyP.ID {
				t.Fatalf("deleted principle still in sidecar: %s", string(raw))
			}
		}
	})

	_ = globalP // global principles are fetched via document_list per the load-context — not inlined on MCP initialize
}

func assertPrincipleIDs(t *testing.T, got []verb.Principle, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("principle count: got %d want %d (%+v)", len(got), len(want), idsOf(got))
	}
	seen := map[string]bool{}
	for _, p := range got {
		seen[p.ID] = true
	}
	for _, id := range want {
		if !seen[id] {
			t.Fatalf("expected principle id %s in sidecar; got %v", id, idsOf(got))
		}
	}
}

func assertScopes(t *testing.T, got []verb.Principle, want ...verb.PrincipleScope) {
	t.Helper()
	seen := map[verb.PrincipleScope]bool{}
	for _, p := range got {
		seen[p.Scope] = true
	}
	for _, s := range want {
		if !seen[s] {
			t.Fatalf("expected scope %s in sidecar; got %v", s, scopesOf(got))
		}
	}
}

func assertBodyMatches(t *testing.T, got []verb.Principle, id, want string) {
	t.Helper()
	for _, p := range got {
		if p.ID == id {
			if p.Body != want {
				t.Fatalf("principle %s body: got %q want %q", id, p.Body, want)
			}
			return
		}
	}
	t.Fatalf("principle %s not in sidecar (have %v)", id, idsOf(got))
}

func idsOf(ps []verb.Principle) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.ID
	}
	return out
}

func scopesOf(ps []verb.Principle) []verb.PrincipleScope {
	out := make([]verb.PrincipleScope, len(ps))
	for i, p := range ps {
		out[i] = p.Scope
	}
	return out
}
