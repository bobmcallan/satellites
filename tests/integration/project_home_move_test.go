//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
	"github.com/chromedp/chromedp"
)

// TestProjectHomeMove is the DOGFOOD for sty_896cebb1 (epic:workspace-admin ·
// project-home-move): an id-present project_update carrying a changed
// workspace_id re-homes a project's single writable workspace under two-sided
// admin authority, preserving readonly mounts and re-evaluating grants.
func TestProjectHomeMove(t *testing.T) {
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
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)

	gAdmin, _ := env.Store.GetUserByID(ctx, "usr_dev_admin") // platform-admin
	srcAdmin, _ := env.Store.CreateUser(ctx, "usr_hm_src", "src@hm.local", "SrcAdmin", "user")
	dstAdmin, _ := env.Store.CreateUser(ctx, "usr_hm_dst", "dst@hm.local", "DstAdmin", "user")

	// Source / destination / mount-holder workspaces; src & dst each owned by
	// their respective admins (owner_user_id + admin member row).
	wsA, _ := wsStore.Create(ctx, srcAdmin.ID, "home-src", now)
	wsB, _ := wsStore.Create(ctx, dstAdmin.ID, "home-dst", now)
	wsC, _ := wsStore.Create(ctx, gAdmin.ID, "mount-holder", now)

	pj, _ := pjStore.Create(ctx, project.CreateInput{WorkspaceID: wsA.ID, Name: "mover", OwnerUserID: srcAdmin.ID}, now)
	// Readonly-mount the project into wsC; this must survive the move untouched.
	if err := wsStore.AddMount(ctx, wsC.ID, pj.ID, gAdmin.ID, now); err != nil {
		t.Fatalf("seed mount: %v", err)
	}

	move := func(c context.Context, projectID, destWs string) error {
		_, err := verb.Dispatch(c, "project_update", json.RawMessage(
			`{"id":"`+projectID+`","workspace_id":"`+destWs+`"}`))
		return err
	}
	writeDoc := func(c context.Context, ws, projectID, name string) error {
		_, err := verb.Dispatch(c, "document_upsert", json.RawMessage(
			`{"name":"`+name+`","scope":"project","workspace_id":"`+ws+`","project_id":"`+projectID+`","body":"x"}`))
		return err
	}
	forbidden := func(t *testing.T, err error) {
		t.Helper()
		if err == nil || !errors.Is(err, verb.ErrForbidden) {
			t.Fatalf("expected ErrForbidden, got %v", err)
		}
	}

	// AC4 (authz): a caller who is admin of the source but NOT the destination
	// is refused before anything moves.
	t.Run("move refused without admin on both sides", func(t *testing.T) {
		forbidden(t, move(authWithUser(ctx, srcAdmin), pj.ID, wsB.ID))
		// Project is still home in wsA.
		if got, _ := pjStore.GetByID(ctx, pj.ID); got.WorkspaceID != wsA.ID {
			t.Fatalf("project moved despite refusal: home=%q", got.WorkspaceID)
		}
	})

	// AC3 (refused cases): non-existent destination, and a destination that
	// already readonly-mounts the project.
	t.Run("move into non-existent workspace refused", func(t *testing.T) {
		if err := move(authWithUser(ctx, gAdmin), pj.ID, "wksp_nope0000"); err == nil {
			t.Fatal("expected error moving into non-existent workspace")
		}
	})
	t.Run("move into a workspace that already mounts the project refused", func(t *testing.T) {
		// gAdmin is admin of both wsA (global) and wsC (owner); wsC mounts pj.
		err := move(authWithUser(ctx, gAdmin), pj.ID, wsC.ID)
		if err == nil {
			t.Fatal("expected error moving into a mounting workspace")
		}
		if got, _ := pjStore.GetByID(ctx, pj.ID); got.WorkspaceID != wsA.ID {
			t.Fatalf("project moved into mounting workspace: home=%q", got.WorkspaceID)
		}
	})

	// AC1/AC2/AC5: a platform-admin moves pj from wsA → wsB.
	if err := move(authWithUser(ctx, gAdmin), pj.ID, wsB.ID); err != nil {
		t.Fatalf("home move: %v", err)
	}
	moved, _ := pjStore.GetByID(ctx, pj.ID)
	if moved.WorkspaceID != wsB.ID {
		t.Fatalf("after move home = %q, want %q", moved.WorkspaceID, wsB.ID)
	}

	// AC2: the readonly mount into wsC is untouched.
	if ok, err := wsStore.MountExists(ctx, wsC.ID, pj.ID); err != nil || !ok {
		t.Fatalf("readonly mount into wsC was lost: ok=%v err=%v", ok, err)
	}

	// AC5 (grants re-evaluate): the destination owner can now write the project;
	// the source owner — who had write only via the old home — can no longer.
	t.Run("writable in the new home, not the old", func(t *testing.T) {
		if err := writeDoc(authWithUser(ctx, dstAdmin), wsB.ID, pj.ID, "by-dst"); err != nil {
			t.Fatalf("destination owner should be able to write the moved project: %v", err)
		}
		forbidden(t, writeDoc(authWithUser(ctx, srcAdmin), wsB.ID, pj.ID, "by-src"))
	})

	// AC6 (chromedp): the destination's workspace page lists the moved project
	// as a home repo.
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), chromedpHeadlessOpts()...)
	t.Cleanup(cancelAlloc)
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	t.Cleanup(cancelBrowser)
	bctx, cancelRun := context.WithTimeout(browserCtx, 60*time.Second)
	t.Cleanup(cancelRun)

	var listedInDest, stillInSource bool
	if err := chromedp.Run(bctx,
		chromedp.Navigate(env.ServerURL+"/login"),
		chromedp.WaitVisible(`form[data-form="login"]`, chromedp.ByQuery),
		chromedp.Click(`button[data-action="dev-login-admin"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
		chromedp.Navigate(env.ServerURL+"/workspaces/"+wsB.ID),
		chromedp.WaitVisible(`[data-section="workspace-projects"]`, chromedp.ByQuery),
		chromedp.Evaluate(`!!document.querySelector('[data-section="workspace-projects"] a[href="/projects/`+pj.ID+`"]')`, &listedInDest),
		chromedp.Navigate(env.ServerURL+"/workspaces/"+wsA.ID),
		chromedp.WaitVisible(`[data-section="workspace-projects"]`, chromedp.ByQuery),
		chromedp.Evaluate(`!!document.querySelector('[data-section="workspace-projects"] a[href="/projects/`+pj.ID+`"]')`, &stillInSource),
	); err != nil {
		t.Fatalf("workspace pages: %v", err)
	}
	if !listedInDest {
		t.Fatalf("destination workspace page does not list the moved project %s", pj.ID)
	}
	if stillInSource {
		t.Fatalf("source workspace page still lists the moved project %s", pj.ID)
	}
}
