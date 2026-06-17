//go:build integration

package integration_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestDocumentStore exercises the doc-substrate primitives against a
// live Postgres: scope coherence, version chain monotonicity, scope
// read-only enforcement on writes, soft-delete, and concurrent-upsert
// serialisation.
func TestDocumentStore(t *testing.T) {
	env := testbootstrap.SetUp(t)
	store := document.New(env.DB)
	ctx := context.Background()

	// Seed a workspace + project so workspace/project scope writes have
	// valid FK targets.
	seedWorkspaceAndProject := func(t *testing.T) (wsID, pjID string) {
		t.Helper()
		wsID = "wksp_doctest"
		pjID = "proj_doctest"
		if _, err := env.DB.Exec(`
            INSERT INTO workspaces (id, name) VALUES ($1, 'doctest')
            ON CONFLICT (id) DO NOTHING
        `, wsID); err != nil {
			t.Fatalf("seed workspace: %v", err)
		}
		if _, err := env.DB.Exec(`
            INSERT INTO projects (id, workspace_id, name) VALUES ($1, $2, 'doctest')
            ON CONFLICT (id) DO NOTHING
        `, pjID, wsID); err != nil {
			t.Fatalf("seed project: %v", err)
		}
		return wsID, pjID
	}

	t.Run("system scope rejects writes from non-seed callers", func(t *testing.T) {
		testbootstrap.Reset(t, env)
		_, _, err := store.Upsert(ctx, document.UpsertInput{
			Key:  document.Key{Scope: document.ScopeSystem, Name: "foo"},
			Body: "hello",
		}, time.Now())
		if !errors.Is(err, document.ErrScopeReadonly) {
			t.Fatalf("expected ErrScopeReadonly, got %v", err)
		}
	})

	t.Run("system scope accepts writes via internal seed", func(t *testing.T) {
		testbootstrap.Reset(t, env)
		doc, v, err := store.Upsert(ctx, document.UpsertInput{
			Key:                document.Key{Scope: document.ScopeSystem, Name: "satellites_client_install"},
			Body:               "seeded body",
			SystemWriteAllowed: true,
			CreatedBy:          "system:seed",
		}, time.Now())
		if err != nil {
			t.Fatalf("seed upsert: %v", err)
		}
		if doc.LatestVersion != 1 || v.Version != 1 {
			t.Fatalf("expected version 1, got doc=%d version=%d", doc.LatestVersion, v.Version)
		}
	})

	t.Run("upsert appends new version rows monotonically", func(t *testing.T) {
		testbootstrap.Reset(t, env)
		wsID, _ := seedWorkspaceAndProject(t)
		key := document.Key{Scope: document.ScopeWorkspace, WorkspaceID: wsID, Name: "release_notes"}

		for i := 1; i <= 3; i++ {
			doc, v, err := store.Upsert(ctx, document.UpsertInput{
				Key: key, Body: "rev " + iToA(i), CreatedBy: "tester",
			}, time.Now())
			if err != nil {
				t.Fatalf("upsert %d: %v", i, err)
			}
			if doc.LatestVersion != i || v.Version != i {
				t.Fatalf("upsert %d: expected version %d, got doc=%d version=%d", i, i, doc.LatestVersion, v.Version)
			}
		}

		res, err := store.Get(ctx, key, document.GetOptions{AllVersions: true})
		if err != nil {
			t.Fatalf("list all: %v", err)
		}
		if len(res.Versions) != 3 {
			t.Fatalf("expected 3 versions, got %d", len(res.Versions))
		}
		for i, v := range res.Versions {
			if v.Version != i+1 {
				t.Fatalf("version[%d] = %d (want %d)", i, v.Version, i+1)
			}
		}
	})

	t.Run("get latest returns highest-version active row", func(t *testing.T) {
		testbootstrap.Reset(t, env)
		wsID, _ := seedWorkspaceAndProject(t)
		key := document.Key{Scope: document.ScopeWorkspace, WorkspaceID: wsID, Name: "settings"}

		for i := 1; i <= 2; i++ {
			if _, _, err := store.Upsert(ctx, document.UpsertInput{
				Key: key, Body: "v" + iToA(i),
			}, time.Now()); err != nil {
				t.Fatalf("upsert %d: %v", i, err)
			}
		}
		res, err := store.Get(ctx, key, document.GetOptions{})
		if err != nil {
			t.Fatalf("get latest: %v", err)
		}
		if len(res.Versions) != 1 {
			t.Fatalf("expected 1 version row, got %d", len(res.Versions))
		}
		if res.Versions[0].Version != 2 || res.Versions[0].Body != "v2" {
			t.Fatalf("unexpected latest: %+v", res.Versions[0])
		}
	})

	t.Run("get specific version returns that exact row", func(t *testing.T) {
		testbootstrap.Reset(t, env)
		wsID, _ := seedWorkspaceAndProject(t)
		key := document.Key{Scope: document.ScopeWorkspace, WorkspaceID: wsID, Name: "settings"}
		for i := 1; i <= 3; i++ {
			if _, _, err := store.Upsert(ctx, document.UpsertInput{
				Key: key, Body: "v" + iToA(i),
			}, time.Now()); err != nil {
				t.Fatalf("upsert %d: %v", i, err)
			}
		}
		res, err := store.Get(ctx, key, document.GetOptions{Version: 2})
		if err != nil {
			t.Fatalf("get v2: %v", err)
		}
		if res.Versions[0].Body != "v2" {
			t.Fatalf("unexpected body: %s", res.Versions[0].Body)
		}
	})

	t.Run("delete is soft and history survives", func(t *testing.T) {
		testbootstrap.Reset(t, env)
		wsID, pjID := seedWorkspaceAndProject(t)
		key := document.Key{Scope: document.ScopeProject, WorkspaceID: wsID, ProjectID: pjID, Name: "rubric"}
		for i := 1; i <= 2; i++ {
			if _, _, err := store.Upsert(ctx, document.UpsertInput{
				Key: key, Body: "v" + iToA(i),
			}, time.Now()); err != nil {
				t.Fatalf("upsert %d: %v", i, err)
			}
		}
		if _, _, err := store.Delete(ctx, key, "tester", false, time.Now()); err != nil {
			t.Fatalf("delete: %v", err)
		}

		// Default latest: not found (tombstoned).
		if _, err := store.Get(ctx, key, document.GetOptions{}); !errors.Is(err, document.ErrNotFound) {
			t.Fatalf("expected ErrNotFound for tombstoned latest, got %v", err)
		}

		// Including deleted: returns the tombstone row.
		res, err := store.Get(ctx, key, document.GetOptions{IncludeDeleted: true})
		if err != nil {
			t.Fatalf("get include-deleted: %v", err)
		}
		if res.Versions[0].Status != document.StatusDeleted {
			t.Fatalf("expected deleted status, got %s", res.Versions[0].Status)
		}

		// AllVersions returns the full chain including the tombstone.
		all, err := store.Get(ctx, key, document.GetOptions{AllVersions: true})
		if err != nil {
			t.Fatalf("get all: %v", err)
		}
		if len(all.Versions) != 3 {
			t.Fatalf("expected 3 versions (2 active + 1 tombstone), got %d", len(all.Versions))
		}
		if all.Versions[2].Status != document.StatusDeleted {
			t.Fatalf("expected last version to be deleted, got %s", all.Versions[2].Status)
		}
	})

	t.Run("delete on system scope rejects from non-seed callers", func(t *testing.T) {
		testbootstrap.Reset(t, env)
		// Seed a system doc via the internal-seed path so the row exists.
		if _, _, err := store.Upsert(ctx, document.UpsertInput{
			Key:                document.Key{Scope: document.ScopeSystem, Name: "system_doc"},
			Body:               "x",
			SystemWriteAllowed: true,
		}, time.Now()); err != nil {
			t.Fatalf("seed: %v", err)
		}
		_, _, err := store.Delete(ctx, document.Key{Scope: document.ScopeSystem, Name: "system_doc"}, "tester", false, time.Now())
		if !errors.Is(err, document.ErrScopeReadonly) {
			t.Fatalf("expected ErrScopeReadonly, got %v", err)
		}
	})

	t.Run("concurrent upserts produce strictly monotonic versions", func(t *testing.T) {
		testbootstrap.Reset(t, env)
		wsID, _ := seedWorkspaceAndProject(t)
		key := document.Key{Scope: document.ScopeWorkspace, WorkspaceID: wsID, Name: "concurrent"}

		const n = 10
		var wg sync.WaitGroup
		errs := make([]error, n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_, _, err := store.Upsert(ctx, document.UpsertInput{
					Key: key, Body: "v" + iToA(i),
				}, time.Now())
				errs[i] = err
			}(i)
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("goroutine %d: %v", i, err)
			}
		}
		res, err := store.Get(ctx, key, document.GetOptions{AllVersions: true})
		if err != nil {
			t.Fatalf("get all: %v", err)
		}
		if len(res.Versions) != n {
			t.Fatalf("expected %d versions, got %d", n, len(res.Versions))
		}
		for i, v := range res.Versions {
			if v.Version != i+1 {
				t.Fatalf("version[%d] = %d (want %d)", i, v.Version, i+1)
			}
		}
	})

	t.Run("skill rows accept document-level tags (sty_598369dd)", func(t *testing.T) {
		testbootstrap.Reset(t, env)
		wsID, pjID := seedWorkspaceAndProject(t)
		key := document.Key{Scope: document.ScopeProject, WorkspaceID: wsID, ProjectID: pjID, Name: "gate-skill"}
		doc, _, err := store.Upsert(ctx, document.UpsertInput{
			Key: key, Type: document.TypeSkill, Body: "skill body", CreatedBy: "tester",
		}, time.Now())
		if err != nil {
			t.Fatalf("skill upsert: %v", err)
		}
		// Before sty_598369dd this rejected with "expected document".
		tagged, err := store.SetDocumentTags(ctx, doc.ID, []string{"kind:gate"}, time.Now())
		if err != nil {
			t.Fatalf("SetDocumentTags on skill row: %v", err)
		}
		if len(tagged.Tags) != 1 || tagged.Tags[0] != "kind:gate" {
			t.Fatalf("tags not persisted on skill: %+v", tagged.Tags)
		}
		// Idempotent re-apply of the same tag set is a no-op, not an error.
		if _, err := store.SetDocumentTags(ctx, doc.ID, []string{"kind:gate"}, time.Now()); err != nil {
			t.Fatalf("re-apply tags: %v", err)
		}
	})

	t.Run("scope-coherence rejected by store before DB", func(t *testing.T) {
		testbootstrap.Reset(t, env)
		_, _, err := store.Upsert(ctx, document.UpsertInput{
			Key:  document.Key{Scope: document.ScopeProject, WorkspaceID: "wksp_x", Name: "x"},
			Body: "x",
		}, time.Now())
		if !errors.Is(err, document.ErrScopeMismatch) {
			t.Fatalf("expected ErrScopeMismatch, got %v", err)
		}
	})
}

func iToA(i int) string {
	// Tiny inline helper to avoid pulling strconv into the test imports
	// for one call-site flavor. Bytes-level int-to-string up to 99.
	if i < 10 {
		return string(rune('0' + i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}
