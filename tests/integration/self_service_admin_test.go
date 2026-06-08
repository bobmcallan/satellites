//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestSelfServiceMemberVerbs pins epic:user-admin sty_3dbd53f0: project_member_*
// and workspace_member_* mutations are authorization-gated to the right role,
// denied for the wrong one.
func TestSelfServiceMemberVerbs(t *testing.T) {
	env := testbootstrap.SetUp(t)
	testbootstrap.Reset(t, env)

	ctx := context.Background()
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	authStore := auth.New(env.DB)
	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	verb.SetAuthStore(authStore)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(pjStore)
	t.Cleanup(func() {
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
	})

	gAdmin, _ := authStore.CreateUser(ctx, "usr_ss_gadmin", "g@ss.local", "G", auth.RoleAdmin)
	wsAdmin, _ := authStore.CreateUser(ctx, "usr_ss_wsadmin", "wa@ss.local", "WA", auth.RoleUser)
	wsMember, _ := authStore.CreateUser(ctx, "usr_ss_member", "m@ss.local", "M", auth.RoleUser)
	pjAdmin, _ := authStore.CreateUser(ctx, "usr_ss_pjadmin", "pa@ss.local", "PA", auth.RoleUser)
	pjWriter, _ := authStore.CreateUser(ctx, "usr_ss_pjwriter", "pw@ss.local", "PW", auth.RoleUser)
	outsider, _ := authStore.CreateUser(ctx, "usr_ss_out", "o@ss.local", "O", auth.RoleUser)
	target, _ := authStore.CreateUser(ctx, "usr_ss_target", "t@ss.local", "T", auth.RoleUser)

	ws, _ := wsStore.Create(ctx, wsAdmin.ID, "ss-ws", now) // wsAdmin → admin member
	_ = wsStore.AddMember(ctx, ws.ID, wsMember.ID, workspace.RoleMember, wsAdmin.ID, now)
	pj, _ := pjStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "ss-pj"}, now)
	_ = pjStore.AddMember(ctx, pj.ID, pjAdmin.ID, project.RoleAdmin, wsAdmin.ID, now)
	_ = pjStore.AddMember(ctx, pj.ID, pjWriter.ID, project.RoleWrite, wsAdmin.ID, now)

	ok := func(t *testing.T, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("expected success, got %v", err)
		}
	}
	denied := func(t *testing.T, err error) {
		t.Helper()
		if err == nil {
			t.Fatal("expected forbidden, got nil")
		}
	}

	pmAdd := func(c context.Context) error {
		_, err := verb.Dispatch(c, "project_member_add", json.RawMessage(
			`{"project_id":"`+pj.ID+`","user_id":"`+target.ID+`","role":"read"}`))
		return err
	}
	pmList := func(c context.Context) error {
		_, err := verb.Dispatch(c, "project_member_list", json.RawMessage(`{"project_id":"`+pj.ID+`"}`))
		return err
	}
	wmAdd := func(c context.Context) error {
		_, err := verb.Dispatch(c, "workspace_member_add", json.RawMessage(
			`{"workspace_id":"`+ws.ID+`","user_id":"`+target.ID+`","role":"member"}`))
		return err
	}
	wmList := func(c context.Context) error {
		_, err := verb.Dispatch(c, "workspace_member_list", json.RawMessage(`{"workspace_id":"`+ws.ID+`"}`))
		return err
	}

	t.Run("project_member_add: admin paths allowed", func(t *testing.T) {
		ok(t, pmAdd(authWithUser(ctx, gAdmin)))
		ok(t, pmAdd(authWithUser(ctx, wsAdmin)))
		ok(t, pmAdd(authWithUser(ctx, pjAdmin)))
	})
	t.Run("project_member_add: non-admin denied", func(t *testing.T) {
		denied(t, pmAdd(authWithUser(ctx, pjWriter)))
		denied(t, pmAdd(authWithUser(ctx, wsMember)))
		denied(t, pmAdd(authWithUser(ctx, outsider)))
	})
	t.Run("project_member_list: members allowed, outsider denied", func(t *testing.T) {
		ok(t, pmList(authWithUser(ctx, pjWriter))) // ≥ read
		ok(t, pmList(authWithUser(ctx, pjAdmin)))
		denied(t, pmList(authWithUser(ctx, outsider)))
	})

	t.Run("workspace_member_add: workspace admin allowed", func(t *testing.T) {
		ok(t, wmAdd(authWithUser(ctx, gAdmin)))
		ok(t, wmAdd(authWithUser(ctx, wsAdmin)))
	})
	t.Run("workspace_member_add: plain member + outsider denied", func(t *testing.T) {
		denied(t, wmAdd(authWithUser(ctx, wsMember)))
		denied(t, wmAdd(authWithUser(ctx, outsider)))
	})
	t.Run("workspace_member_list: member allowed, outsider denied", func(t *testing.T) {
		ok(t, wmList(authWithUser(ctx, wsMember)))
		denied(t, wmList(authWithUser(ctx, outsider)))
	})
}
