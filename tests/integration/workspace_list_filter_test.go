//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestWorkspaceListIdentityFilter pins epic:user-admin sty_14cf17e3 AC#1/#4:
// workspace_list returns ALL workspaces to a global admin, but only the
// caller's own workspaces to a non-admin.
func TestWorkspaceListIdentityFilter(t *testing.T) {
	env := testbootstrap.SetUp(t)
	testbootstrap.Reset(t, env)

	ctx := context.Background()
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	authStore := auth.New(env.DB)
	wsStore := workspace.New(env.DB)
	verb.SetAuthStore(authStore)
	verb.SetWorkspaceStore(wsStore)
	t.Cleanup(func() {
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
	})

	gAdmin, _ := authStore.CreateUser(ctx, "usr_wl_admin", "a@wl.local", "A", auth.RoleAdmin)
	plain, _ := authStore.CreateUser(ctx, "usr_wl_plain", "p@wl.local", "P", auth.RoleUser)
	mineWS, _ := wsStore.Create(ctx, plain.ID, "plain-ws", now)   // plain is a member
	otherWS, _ := wsStore.Create(ctx, gAdmin.ID, "admin-ws", now) // plain is NOT a member

	list := func(u *auth.User) map[string]bool {
		raw, err := verb.Dispatch(authWithUser(ctx, u), "workspace_list", nil)
		if err != nil {
			t.Fatalf("workspace_list: %v", err)
		}
		var resp verb.WorkspaceListResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		ids := map[string]bool{}
		for _, w := range resp.Workspaces {
			ids[w.ID] = true
		}
		return ids
	}

	adminSees := list(gAdmin)
	if !adminSees[mineWS.ID] || !adminSees[otherWS.ID] {
		t.Fatalf("global admin should see all workspaces, saw %v", adminSees)
	}

	plainSees := list(plain)
	if !plainSees[mineWS.ID] {
		t.Fatal("plain user should see their own workspace")
	}
	if plainSees[otherWS.ID] {
		t.Fatal("plain user must NOT see a workspace they are not a member of")
	}
}
