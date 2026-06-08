//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestProjectScopedAuthorization pins the epic:user-admin sty_c96cc77f
// capability matrix: project-scoped reads need ≥read, writes need ≥write,
// project admin ops need admin — resolved by effectiveProjectRole over global
// admin / workspace owner-admin / explicit project_members / api-key binding.
func TestProjectScopedAuthorization(t *testing.T) {
	env := testbootstrap.SetUp(t)
	testbootstrap.Reset(t, env)

	ctx := context.Background()
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	authStore := auth.New(env.DB)
	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	docStore := document.New(env.DB)
	verb.SetAuthStore(authStore)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(pjStore)
	verb.SetDocumentStore(docStore)
	t.Cleanup(func() {
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
		verb.SetDocumentStore(nil)
	})

	gAdmin, _ := authStore.CreateUser(ctx, "usr_az_gadmin", "gadmin@az.local", "GAdmin", auth.RoleAdmin)
	wsAdmin, _ := authStore.CreateUser(ctx, "usr_az_wsadmin", "wsadmin@az.local", "WsAdmin", auth.RoleUser)
	reader, _ := authStore.CreateUser(ctx, "usr_az_reader", "reader@az.local", "Reader", auth.RoleUser)
	writer, _ := authStore.CreateUser(ctx, "usr_az_writer", "writer@az.local", "Writer", auth.RoleUser)
	outsider, _ := authStore.CreateUser(ctx, "usr_az_out", "out@az.local", "Outsider", auth.RoleUser)
	keyUser, _ := authStore.CreateUser(ctx, "usr_az_key", "key@az.local", "KeyUser", auth.RoleUser)

	ws, _ := wsStore.Create(ctx, wsAdmin.ID, "az-ws", now) // wsAdmin → 'admin' member
	pj, _ := pjStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "az-pj"}, now)
	otherPj, _ := pjStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "az-other"}, now)
	_ = pjStore.AddMember(ctx, pj.ID, reader.ID, project.RoleRead, wsAdmin.ID, now)
	_ = pjStore.AddMember(ctx, pj.ID, writer.ID, project.RoleWrite, wsAdmin.ID, now)

	upsert := func(c context.Context, name string) error {
		_, err := verb.Dispatch(c, "document_upsert", json.RawMessage(
			`{"name":"`+name+`","scope":"project","workspace_id":"`+ws.ID+`","project_id":"`+pj.ID+`","body":"x"}`))
		return err
	}
	get := func(c context.Context, name string) error {
		_, err := verb.Dispatch(c, "document_get", json.RawMessage(
			`{"name":"`+name+`","scope":"project","workspace_id":"`+ws.ID+`","project_id":"`+pj.ID+`"}`))
		return err
	}
	forbidden := func(t *testing.T, err error) {
		t.Helper()
		if err == nil {
			t.Fatal("expected ErrForbidden, got nil")
		}
	}
	okErr := func(t *testing.T, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("expected success, got %v", err)
		}
	}

	apiKeyCtx := func(u *auth.User, boundProject string) context.Context {
		c := authWithUser(ctx, u)
		c = auth.WithAPIKeyRole(c, auth.APIKeyRoleExecutor)
		return auth.WithAPIKeyProject(c, boundProject)
	}

	t.Run("write allowed: global admin, workspace admin, project writer, api-key-bound", func(t *testing.T) {
		okErr(t, upsert(authWithUser(ctx, gAdmin), "w-gadmin"))
		okErr(t, upsert(authWithUser(ctx, wsAdmin), "w-wsadmin"))
		okErr(t, upsert(authWithUser(ctx, writer), "w-writer"))
		okErr(t, upsert(apiKeyCtx(keyUser, pj.ID), "w-apikey"))
	})

	t.Run("write denied: project reader, outsider, api-key bound elsewhere", func(t *testing.T) {
		forbidden(t, upsert(authWithUser(ctx, reader), "w-reader"))
		forbidden(t, upsert(authWithUser(ctx, outsider), "w-outsider"))
		forbidden(t, upsert(apiKeyCtx(keyUser, otherPj.ID), "w-wrongkey"))
	})

	t.Run("read allowed for reader; denied for outsider", func(t *testing.T) {
		// seed a doc as admin first
		okErr(t, upsert(authWithUser(ctx, gAdmin), "r-doc"))
		okErr(t, get(authWithUser(ctx, reader), "r-doc"))
		forbidden(t, get(authWithUser(ctx, outsider), "r-doc"))
	})

	t.Run("project_update needs admin", func(t *testing.T) {
		upd := func(c context.Context) error {
			_, err := verb.Dispatch(c, "project_update", json.RawMessage(
				`{"id":"`+pj.ID+`","description":"touched"}`))
			return err
		}
		okErr(t, upd(authWithUser(ctx, gAdmin)))
		okErr(t, upd(authWithUser(ctx, wsAdmin)))
		forbidden(t, upd(authWithUser(ctx, writer))) // write < admin
		forbidden(t, upd(authWithUser(ctx, outsider)))
	})

	t.Run("project_create needs workspace admin", func(t *testing.T) {
		create := func(c context.Context, name string) error {
			_, err := verb.Dispatch(c, "project_create", json.RawMessage(
				`{"name":"`+name+`","workspace_id":"`+ws.ID+`"}`))
			return err
		}
		okErr(t, create(authWithUser(ctx, gAdmin), "cp-gadmin"))
		okErr(t, create(authWithUser(ctx, wsAdmin), "cp-wsadmin"))
		forbidden(t, create(authWithUser(ctx, outsider), "cp-outsider"))
	})
}
