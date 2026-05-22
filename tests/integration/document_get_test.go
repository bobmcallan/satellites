//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/config/seed"
	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestDocumentGet exercises the verb end-to-end: seed the embedded
// install-schema markdown as a scope=system document, verify byte
// equivalence, then exercise auth scoping, version modes, and inherit
// cascade against the live Postgres backend.
func TestDocumentGet(t *testing.T) {
	env := testbootstrap.SetUp(t)

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
		t.Fatalf("truncate auth: %v", err)
	}
	if err := authStore.DevSeed(context.Background()); err != nil {
		t.Fatalf("dev seed: %v", err)
	}
	admin, err := authStore.GetUserByEmail(context.Background(), auth.DevAdminEmail)
	if err != nil {
		t.Fatalf("lookup admin: %v", err)
	}
	user, err := authStore.GetUserByEmail(context.Background(), auth.DevUserEmail)
	if err != nil {
		t.Fatalf("lookup user: %v", err)
	}

	ctxAdmin := authWithUser(context.Background(), admin)
	ctxUser := authWithUser(context.Background(), user)

	t.Run("seeded install schema is byte-equivalent to embedded source", func(t *testing.T) {
		testbootstrap.Reset(t, env)
		body := string(seed.ClientInstallMarkdown())
		if err := document.SeedSystem(context.Background(), docStore, "satellites_client_install", body, "system:seed", time.Now()); err != nil {
			t.Fatalf("seed: %v", err)
		}
		req := `{"name":"satellites_client_install","scope":"system"}`
		raw, err := verb.Dispatch(ctxAdmin, "document_get", json.RawMessage(req))
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		var got verb.DocumentGetResponse
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Versions) != 1 {
			t.Fatalf("expected 1 version, got %d", len(got.Versions))
		}
		if got.Versions[0].Body != body {
			t.Fatalf("body diverged from embedded source")
		}
		if got.ResolvedScope != "system" {
			t.Fatalf("resolved_scope=%s want system", got.ResolvedScope)
		}
	})

	t.Run("seed is idempotent (no new version on equal body)", func(t *testing.T) {
		testbootstrap.Reset(t, env)
		body := "stable content"
		for i := 0; i < 3; i++ {
			if err := document.SeedSystem(context.Background(), docStore, "fixed", body, "system:seed", time.Now()); err != nil {
				t.Fatalf("seed pass %d: %v", i, err)
			}
		}
		res, err := docStore.Get(context.Background(), document.Key{Scope: document.ScopeSystem, Name: "fixed"}, document.GetOptions{AllVersions: true})
		if err != nil {
			t.Fatalf("get all: %v", err)
		}
		if len(res.Versions) != 1 {
			t.Fatalf("idempotent seed expected 1 version, got %d", len(res.Versions))
		}
	})

	t.Run("version modes — latest, integer, all", func(t *testing.T) {
		testbootstrap.Reset(t, env)
		key := document.Key{Scope: document.ScopeSystem, Name: "versioned"}
		for _, b := range []string{"v1", "v2", "v3"} {
			if _, _, err := docStore.Upsert(context.Background(), document.UpsertInput{
				Key: key, Body: b, ViaInternalSeed: true,
			}, time.Now()); err != nil {
				t.Fatalf("upsert %s: %v", b, err)
			}
		}

		check := func(req string, wantBodies []string) {
			raw, err := verb.Dispatch(ctxAdmin, "document_get", json.RawMessage(req))
			if err != nil {
				t.Fatalf("dispatch %s: %v", req, err)
			}
			var got verb.DocumentGetResponse
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			if len(got.Versions) != len(wantBodies) {
				t.Fatalf("%s: got %d versions, want %d", req, len(got.Versions), len(wantBodies))
			}
			for i, b := range wantBodies {
				if got.Versions[i].Body != b {
					t.Fatalf("%s: version[%d].body=%q want %q", req, i, got.Versions[i].Body, b)
				}
			}
		}

		check(`{"name":"versioned","scope":"system"}`, []string{"v3"})
		check(`{"name":"versioned","scope":"system","version":"latest"}`, []string{"v3"})
		check(`{"name":"versioned","scope":"system","version":"2"}`, []string{"v2"})
		check(`{"name":"versioned","scope":"system","version":"all"}`, []string{"v1", "v2", "v3"})
	})

	t.Run("workspace read denies non-member with 403 sentinel", func(t *testing.T) {
		testbootstrap.Reset(t, env)
		// Two workspaces; admin joins one, user joins the other.
		wsA, err := wsStore.Create(context.Background(), admin.ID, "A", time.Now())
		if err != nil {
			t.Fatalf("create wsA: %v", err)
		}
		wsB, err := wsStore.Create(context.Background(), user.ID, "B", time.Now())
		if err != nil {
			t.Fatalf("create wsB: %v", err)
		}
		if err := wsStore.AddMember(context.Background(), wsA.ID, admin.ID, workspace.RoleAdmin, admin.ID, time.Now()); err != nil {
			t.Fatalf("add admin to A: %v", err)
		}
		if err := wsStore.AddMember(context.Background(), wsB.ID, user.ID, workspace.RoleAdmin, user.ID, time.Now()); err != nil {
			t.Fatalf("add user to B: %v", err)
		}

		// Workspace-scoped doc in A.
		if _, _, err := docStore.Upsert(context.Background(), document.UpsertInput{
			Key:  document.Key{Scope: document.ScopeWorkspace, WorkspaceID: wsA.ID, Name: "policy"},
			Body: "secret",
		}, time.Now()); err != nil {
			t.Fatalf("upsert in A: %v", err)
		}

		// User (not a member of A) reads → 403, not 404.
		_, err = verb.Dispatch(ctxUser, "document_get", json.RawMessage(
			`{"name":"policy","scope":"workspace","workspace_id":"`+wsA.ID+`"}`))
		if err == nil {
			t.Fatal("expected forbidden error")
		}
		if !errors.Is(err, verb.ErrForbidden) {
			t.Fatalf("expected ErrForbidden, got %v", err)
		}
		if errors.Is(err, verb.ErrNotFound) {
			t.Fatal("must not leak NotFound when caller lacks membership")
		}

		// Admin (member of A) reads OK.
		raw, err := verb.Dispatch(ctxAdmin, "document_get", json.RawMessage(
			`{"name":"policy","scope":"workspace","workspace_id":"`+wsA.ID+`"}`))
		if err != nil {
			t.Fatalf("admin read: %v", err)
		}
		if !strings.Contains(string(raw), "secret") {
			t.Fatalf("body missing in response: %s", raw)
		}
	})

	t.Run("inherit cascade project → workspace → system", func(t *testing.T) {
		testbootstrap.Reset(t, env)
		ws, err := wsStore.Create(context.Background(), admin.ID, "X", time.Now())
		if err != nil {
			t.Fatalf("create ws: %v", err)
		}
		if err := wsStore.AddMember(context.Background(), ws.ID, admin.ID, workspace.RoleAdmin, admin.ID, time.Now()); err != nil {
			t.Fatalf("add admin: %v", err)
		}
		if _, err := env.DB.Exec(`INSERT INTO projects (id, workspace_id, name) VALUES ($1, $2, 'p')`, "proj_inherit", ws.ID); err != nil {
			t.Fatalf("seed project: %v", err)
		}

		// Only seed at system scope; project + workspace are missing.
		if err := document.SeedSystem(context.Background(), docStore, "cascade", "system-body", "system:seed", time.Now()); err != nil {
			t.Fatalf("seed system: %v", err)
		}

		// Project-scope request with inherit=true falls back through workspace to system.
		raw, err := verb.Dispatch(ctxAdmin, "document_get", json.RawMessage(
			`{"name":"cascade","scope":"project","workspace_id":"`+ws.ID+`","project_id":"proj_inherit","inherit":true}`))
		if err != nil {
			t.Fatalf("inherit dispatch: %v", err)
		}
		var got verb.DocumentGetResponse
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if got.ResolvedScope != "system" {
			t.Fatalf("resolved_scope=%s want system", got.ResolvedScope)
		}
		if got.Versions[0].Body != "system-body" {
			t.Fatalf("body=%q want system-body", got.Versions[0].Body)
		}

		// inherit=false misses with NotFound — no fallback.
		_, err = verb.Dispatch(ctxAdmin, "document_get", json.RawMessage(
			`{"name":"cascade","scope":"project","workspace_id":"`+ws.ID+`","project_id":"proj_inherit"}`))
		if err == nil {
			t.Fatal("expected NotFound without inherit")
		}
		if !errors.Is(err, verb.ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("system scope reads without auth context", func(t *testing.T) {
		testbootstrap.Reset(t, env)
		if err := document.SeedSystem(context.Background(), docStore, "open", "public", "system:seed", time.Now()); err != nil {
			t.Fatalf("seed: %v", err)
		}
		// No user attached to ctx; system reads should still work.
		_, err := verb.Dispatch(context.Background(), "document_get", json.RawMessage(`{"name":"open","scope":"system"}`))
		if err != nil {
			t.Fatalf("system read without user: %v", err)
		}
	})

	t.Run("workspace read without bearer is unauthorized", func(t *testing.T) {
		testbootstrap.Reset(t, env)
		ws, err := wsStore.Create(context.Background(), admin.ID, "Y", time.Now())
		if err != nil {
			t.Fatalf("create ws: %v", err)
		}
		if _, _, err := docStore.Upsert(context.Background(), document.UpsertInput{
			Key: document.Key{Scope: document.ScopeWorkspace, WorkspaceID: ws.ID, Name: "x"}, Body: "y",
		}, time.Now()); err != nil {
			t.Fatalf("upsert: %v", err)
		}
		_, err = verb.Dispatch(context.Background(), "document_get", json.RawMessage(
			`{"name":"x","scope":"workspace","workspace_id":"`+ws.ID+`"}`))
		if !errors.Is(err, verb.ErrUnauthorized) {
			t.Fatalf("expected ErrUnauthorized, got %v", err)
		}
	})
}
