//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestDocumentWriteVerbs exercises document_upsert + document_delete
// end-to-end against a live Postgres + authenticated context, asserting
// every acceptance criterion of S3:
//
//  1. upsert/delete registered and reject scope=system with ErrForbidden.
//  2. workspace/project writes require workspace membership (non-member
//     → ErrForbidden).
//  3. Multiple upserts produce strictly monotonic versions.
//  4. Delete tombstones the latest; default get returns NotFound but
//     version=all preserves the whole chain.
func TestDocumentWriteVerbs(t *testing.T) {
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

	t.Run("system upsert rejected with 403", func(t *testing.T) {
		testbootstrap.Reset(t, env)
		_, err := verb.Dispatch(ctxAdmin, "document_upsert",
			json.RawMessage(`{"name":"x","scope":"system","body":"y"}`))
		if !errors.Is(err, verb.ErrForbidden) {
			t.Fatalf("expected ErrForbidden, got %v", err)
		}
	})

	t.Run("workspace upsert by non-member rejected with 403", func(t *testing.T) {
		testbootstrap.Reset(t, env)
		ws, err := wsStore.Create(context.Background(), admin.ID, "owners-only", time.Now())
		if err != nil {
			t.Fatalf("create ws: %v", err)
		}
		if err := wsStore.AddMember(context.Background(), ws.ID, admin.ID, workspace.RoleAdmin, admin.ID, time.Now()); err != nil {
			t.Fatalf("add admin: %v", err)
		}
		// user is NOT a member of ws.
		_, err = verb.Dispatch(ctxUser, "document_upsert", json.RawMessage(
			`{"name":"policy","scope":"workspace","workspace_id":"`+ws.ID+`","body":"hidden"}`))
		if !errors.Is(err, verb.ErrForbidden) {
			t.Fatalf("expected ErrForbidden, got %v", err)
		}
	})

	t.Run("multi-upsert creates monotonic versions", func(t *testing.T) {
		testbootstrap.Reset(t, env)
		ws, err := wsStore.Create(context.Background(), admin.ID, "alpha", time.Now())
		if err != nil {
			t.Fatalf("create ws: %v", err)
		}
		if err := wsStore.AddMember(context.Background(), ws.ID, admin.ID, workspace.RoleAdmin, admin.ID, time.Now()); err != nil {
			t.Fatalf("add admin: %v", err)
		}
		for i, body := range []string{"v1", "v2", "v3", "v4"} {
			raw, err := verb.Dispatch(ctxAdmin, "document_upsert", json.RawMessage(
				`{"name":"runbook","scope":"workspace","workspace_id":"`+ws.ID+`","body":"`+body+`"}`))
			if err != nil {
				t.Fatalf("upsert %d: %v", i+1, err)
			}
			var got verb.DocumentUpsertResponse
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			if got.Version.Version != i+1 {
				t.Fatalf("upsert %d returned version=%d (want %d)", i+1, got.Version.Version, i+1)
			}
		}
		raw, err := verb.Dispatch(ctxAdmin, "document_get", json.RawMessage(
			`{"name":"runbook","scope":"workspace","workspace_id":"`+ws.ID+`","version":"all"}`))
		if err != nil {
			t.Fatalf("get all: %v", err)
		}
		var got verb.DocumentGetResponse
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Versions) != 4 {
			t.Fatalf("expected 4 versions, got %d", len(got.Versions))
		}
	})

	t.Run("delete tombstones latest; history preserved via version=all", func(t *testing.T) {
		testbootstrap.Reset(t, env)
		ws, err := wsStore.Create(context.Background(), admin.ID, "beta", time.Now())
		if err != nil {
			t.Fatalf("create ws: %v", err)
		}
		if err := wsStore.AddMember(context.Background(), ws.ID, admin.ID, workspace.RoleAdmin, admin.ID, time.Now()); err != nil {
			t.Fatalf("add admin: %v", err)
		}

		// Three upserts then a delete.
		for _, body := range []string{"a", "b", "c"} {
			if _, err := verb.Dispatch(ctxAdmin, "document_upsert", json.RawMessage(
				`{"name":"target","scope":"workspace","workspace_id":"`+ws.ID+`","body":"`+body+`"}`)); err != nil {
				t.Fatalf("upsert %s: %v", body, err)
			}
		}
		if _, err := verb.Dispatch(ctxAdmin, "document_delete", json.RawMessage(
			`{"name":"target","scope":"workspace","workspace_id":"`+ws.ID+`"}`)); err != nil {
			t.Fatalf("delete: %v", err)
		}

		// Default get → NotFound.
		_, err = verb.Dispatch(ctxAdmin, "document_get", json.RawMessage(
			`{"name":"target","scope":"workspace","workspace_id":"`+ws.ID+`"}`))
		if !errors.Is(err, verb.ErrNotFound) {
			t.Fatalf("expected ErrNotFound after delete, got %v", err)
		}

		// version=all surfaces 4 versions (3 active + 1 tombstone).
		raw, err := verb.Dispatch(ctxAdmin, "document_get", json.RawMessage(
			`{"name":"target","scope":"workspace","workspace_id":"`+ws.ID+`","version":"all"}`))
		if err != nil {
			t.Fatalf("get all: %v", err)
		}
		var got verb.DocumentGetResponse
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Versions) != 4 {
			t.Fatalf("expected 4 versions, got %d", len(got.Versions))
		}
		if got.Versions[3].Status != document.StatusDeleted {
			t.Fatalf("expected tombstone at version 4, got %s", got.Versions[3].Status)
		}
	})

	t.Run("delete missing document returns NotFound", func(t *testing.T) {
		testbootstrap.Reset(t, env)
		ws, err := wsStore.Create(context.Background(), admin.ID, "gamma", time.Now())
		if err != nil {
			t.Fatalf("create ws: %v", err)
		}
		if err := wsStore.AddMember(context.Background(), ws.ID, admin.ID, workspace.RoleAdmin, admin.ID, time.Now()); err != nil {
			t.Fatalf("add admin: %v", err)
		}
		_, err = verb.Dispatch(ctxAdmin, "document_delete", json.RawMessage(
			`{"name":"ghost","scope":"workspace","workspace_id":"`+ws.ID+`"}`))
		if !errors.Is(err, verb.ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})
}
