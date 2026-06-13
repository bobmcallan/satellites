//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/mcpserver"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
	"github.com/chromedp/chromedp"
)

// TestWorkspaceArchive is the DOGFOOD for sty_f5c08ea0 (epic:workspace-admin-followup
// · workspace-archive): a reversible soft-archive verb that drops a workspace
// from normal list/read paths while retaining the record, gated by owner-or-
// platform-admin authz, with no-orphan guardrails — and NO hard-delete on the
// MCP surface.
func TestWorkspaceArchive(t *testing.T) {
	env := testbootstrap.SetUp(t)
	testbootstrap.Reset(t, env)

	ctx := context.Background()
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)

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

	gAdmin, _ := authStore.CreateUser(ctx, "usr_arc_gadmin", "gadmin@arc.local", "GAdmin", auth.RoleAdmin)
	owner, _ := authStore.CreateUser(ctx, "usr_arc_owner", "owner@arc.local", "Owner", auth.RoleUser)
	other, _ := authStore.CreateUser(ctx, "usr_arc_other", "other@arc.local", "Other", auth.RoleUser)

	archive := func(c context.Context, body string) (workspace.Workspace, error) {
		raw, err := verb.Dispatch(c, "workspace_archive", json.RawMessage(body))
		if err != nil {
			return workspace.Workspace{}, err
		}
		var w workspace.Workspace
		if err := json.Unmarshal(raw, &w); err != nil {
			return workspace.Workspace{}, err
		}
		return w, nil
	}
	listIDs := func(c context.Context) map[string]bool {
		raw, err := verb.Dispatch(c, "workspace_list", nil)
		if err != nil {
			t.Fatalf("workspace_list: %v", err)
		}
		var resp verb.WorkspaceListResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		ids := map[string]bool{}
		for _, w := range resp.Workspaces {
			ids[w.ID] = true
		}
		return ids
	}

	// AC1/AC2: the owner archives a home-project-free workspace. It flips to
	// archived, drops from the owner's list, but the record is retained — and a
	// global platform-admin still sees it (recoverability safety net).
	ws, _ := wsStore.Create(ctx, owner.ID, "to-archive", now)
	if got := listIDs(authWithUser(ctx, owner)); !got[ws.ID] {
		t.Fatalf("precondition: owner should see active workspace %s; got %v", ws.ID, got)
	}
	archived, err := archive(authWithUser(ctx, owner), `{"workspace_id":"`+ws.ID+`"}`)
	if err != nil {
		t.Fatalf("owner archive: %v", err)
	}
	if archived.Status != workspace.StatusArchived {
		t.Fatalf("archived status = %q, want %q", archived.Status, workspace.StatusArchived)
	}
	if got := listIDs(authWithUser(ctx, owner)); got[ws.ID] {
		t.Fatalf("archived workspace must drop from owner's list; got %v", got)
	}
	if got := listIDs(authWithUser(ctx, gAdmin)); !got[ws.ID] {
		t.Fatalf("global admin must retain visibility of archived workspace; got %v", got)
	}
	if rec, err := wsStore.GetByID(ctx, ws.ID); err != nil || rec.Status != workspace.StatusArchived {
		t.Fatalf("archived record must be retained; status=%q err=%v", rec.Status, err)
	}
	// workspace_get hides it from the non-admin owner (portal → 404) but not the admin.
	if _, err := verb.Dispatch(authWithUser(ctx, owner), "workspace_get", mustJSON(t, verb.WorkspaceGetRequest{ID: ws.ID})); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("non-admin workspace_get of archived ws should be ErrNotFound; got %v", err)
	}
	if _, err := verb.Dispatch(authWithUser(ctx, gAdmin), "workspace_get", mustJSON(t, verb.WorkspaceGetRequest{ID: ws.ID})); err != nil {
		t.Fatalf("global admin workspace_get of archived ws should succeed; got %v", err)
	}

	// AC2: restore reverses the archive — the workspace returns to the owner's list.
	restored, err := archive(authWithUser(ctx, owner), `{"workspace_id":"`+ws.ID+`","restore":true}`)
	if err != nil {
		t.Fatalf("owner restore: %v", err)
	}
	if restored.Status != workspace.StatusActive {
		t.Fatalf("restored status = %q, want %q", restored.Status, workspace.StatusActive)
	}
	if got := listIDs(authWithUser(ctx, owner)); !got[ws.ID] {
		t.Fatalf("restored workspace must reappear in owner's list; got %v", got)
	}

	// AC3: a non-owner / non-admin archive attempt is refused ErrForbidden.
	t.Run("non-owner archive refused", func(t *testing.T) {
		_, err := archive(authWithUser(ctx, other), `{"workspace_id":"`+ws.ID+`"}`)
		if !errors.Is(err, verb.ErrForbidden) {
			t.Fatalf("expected ErrForbidden, got %v", err)
		}
	})

	// AC4: the default workspace cannot be archived.
	t.Run("default workspace archive refused", func(t *testing.T) {
		def, _ := wsStore.Create(ctx, owner.ID, "the-default", now)
		if _, err := wsStore.SetDefault(ctx, def.ID, now); err != nil {
			t.Fatalf("set default: %v", err)
		}
		_, err := archive(authWithUser(ctx, owner), `{"workspace_id":"`+def.ID+`"}`)
		if !errors.Is(err, verb.ErrBadRequest) || !strings.Contains(err.Error(), "default") {
			t.Fatalf("default archive should be refused (ErrBadRequest mentioning default); got %v", err)
		}
	})

	// AC4: a workspace that still holds a home (writable) project cannot be archived.
	t.Run("home-project archive refused", func(t *testing.T) {
		homeWS, _ := wsStore.Create(ctx, owner.ID, "holds-a-repo", now)
		if _, err := pjStore.Create(ctx, project.CreateInput{WorkspaceID: homeWS.ID, Name: "homed-repo"}, now); err != nil {
			t.Fatalf("create home project: %v", err)
		}
		_, err := archive(authWithUser(ctx, owner), `{"workspace_id":"`+homeWS.ID+`"}`)
		if !errors.Is(err, verb.ErrBadRequest) || !strings.Contains(err.Error(), "home project") {
			t.Fatalf("home-project archive should be refused with a clear error; got %v", err)
		}
	})

	// AC4: readonly mounts INTO the workspace are cleared on archive — no live
	// mount points at an archived workspace.
	t.Run("mounts into archived workspace cleared", func(t *testing.T) {
		elsewhere, _ := wsStore.Create(ctx, gAdmin.ID, "mount-source", now)
		mounted, err := pjStore.Create(ctx, project.CreateInput{WorkspaceID: elsewhere.ID, Name: "mounted-repo"}, now)
		if err != nil {
			t.Fatalf("create mounted project: %v", err)
		}
		target, _ := wsStore.Create(ctx, owner.ID, "mount-target", now)
		if err := wsStore.AddMount(ctx, target.ID, mounted.ID, owner.ID, now); err != nil {
			t.Fatalf("add mount: %v", err)
		}
		if _, err := archive(authWithUser(ctx, owner), `{"workspace_id":"`+target.ID+`"}`); err != nil {
			t.Fatalf("archive with cleanable mount: %v", err)
		}
		mounts, err := wsStore.ListMounts(ctx, target.ID)
		if err != nil {
			t.Fatalf("list mounts: %v", err)
		}
		if len(mounts) != 0 {
			t.Fatalf("mounts into archived workspace must be cleared; got %v", mounts)
		}
	})

	// AC4: archiving an already-archived workspace is an idempotent no-op.
	t.Run("archive is idempotent", func(t *testing.T) {
		idem, _ := wsStore.Create(ctx, owner.ID, "idem-ws", now)
		if _, err := archive(authWithUser(ctx, owner), `{"workspace_id":"`+idem.ID+`"}`); err != nil {
			t.Fatalf("first archive: %v", err)
		}
		again, err := archive(authWithUser(ctx, owner), `{"workspace_id":"`+idem.ID+`"}`)
		if err != nil {
			t.Fatalf("idempotent re-archive should be a no-op, got %v", err)
		}
		if again.Status != workspace.StatusArchived {
			t.Fatalf("re-archive status = %q, want archived", again.Status)
		}
	})

	// AC5: the MCP surface exposes workspace_archive to an admin but carries NO
	// destructive workspace-delete verb.
	t.Run("no hard-delete verb on the MCP surface", func(t *testing.T) {
		s := mcpserver.New()
		resp := s.HandleMessage(authWithUser(ctx, gAdmin), json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		raw, _ := json.Marshal(resp)
		var parsed struct {
			Result struct {
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
			} `json:"result"`
		}
		if err := json.Unmarshal(raw, &parsed); err != nil {
			t.Fatalf("unmarshal tools/list: %v (%s)", err, raw)
		}
		names := map[string]bool{}
		for _, tl := range parsed.Result.Tools {
			names[tl.Name] = true
			if strings.Contains(tl.Name, "workspace") && strings.Contains(tl.Name, "delete") {
				t.Fatalf("destructive workspace-delete verb %q must NOT be on the MCP surface", tl.Name)
			}
		}
		if !names["workspace_archive"] {
			t.Fatalf("admin's tools/list missing workspace_archive; got %v", names)
		}
	})
}

// TestWorkspaceArchive_Chromedp is the portal DOGFOOD: a non-admin owner's
// archived workspace drops from the /workspaces list and its detail page is
// gone (404) in the real browser.
func TestWorkspaceArchive_Chromedp(t *testing.T) {
	env := testbootstrap.SetUpWithServer(t)

	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	docStore := document.New(env.DB)
	verb.SetAuthStore(env.Store)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(pjStore)
	verb.SetDocumentStore(docStore)
	t.Cleanup(func() {
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
		verb.SetDocumentStore(nil)
	})

	ctx := context.Background()
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	devUser, err := env.Store.GetUserByID(ctx, "usr_dev_user")
	if err != nil {
		t.Fatalf("get dev user: %v", err)
	}
	// Owned by the (non-admin) dev user so the USER portal session lists it.
	ws, err := wsStore.Create(ctx, devUser.ID, "portal-archive-demo", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), chromedpHeadlessOpts()...)
	t.Cleanup(cancelAlloc)
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	t.Cleanup(cancelBrowser)
	bctx, cancelRun := context.WithTimeout(browserCtx, 60*time.Second)
	t.Cleanup(cancelRun)

	// Log in as the non-admin dev user; the active workspace is listed.
	var linkBefore bool
	if err := chromedp.Run(bctx,
		chromedp.Navigate(env.ServerURL+"/login"),
		chromedp.WaitVisible(`form[data-form="login"]`, chromedp.ByQuery),
		chromedp.Click(`button[data-action="dev-login-user"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
		chromedp.Navigate(env.ServerURL+"/workspaces"),
		chromedp.WaitVisible(`[data-section="workspaces-list"]`, chromedp.ByQuery),
		chromedp.Evaluate(`!!document.querySelector('a[href="/workspaces/`+ws.ID+`"]')`, &linkBefore),
	); err != nil {
		t.Fatalf("workspaces list (before): %v", err)
	}
	if !linkBefore {
		t.Fatalf("active workspace %s not listed before archive", ws.ID)
	}

	// Archive it as the owner via the real verb path.
	if _, err := verb.Dispatch(authWithUser(ctx, devUser), "workspace_archive", mustJSON(t, verb.WorkspaceArchiveRequest{WorkspaceID: ws.ID})); err != nil {
		t.Fatalf("archive: %v", err)
	}

	// The list no longer carries it, and the detail page is gone (no detail section).
	var linkAfter, detailPresent bool
	if err := chromedp.Run(bctx,
		chromedp.Navigate(env.ServerURL+"/workspaces"),
		chromedp.WaitVisible(`[data-section="workspaces-list"]`, chromedp.ByQuery),
		chromedp.Evaluate(`!!document.querySelector('a[href="/workspaces/`+ws.ID+`"]')`, &linkAfter),
		chromedp.Navigate(env.ServerURL+"/workspaces/"+ws.ID),
		chromedp.Evaluate(`!!document.querySelector('[data-section="workspace-detail"]')`, &detailPresent),
	); err != nil {
		t.Fatalf("workspaces list (after): %v", err)
	}
	if linkAfter {
		t.Fatalf("archived workspace %s must drop from the /workspaces page", ws.ID)
	}
	if detailPresent {
		t.Fatalf("archived workspace detail page must be gone (404), but the detail section rendered")
	}
}
