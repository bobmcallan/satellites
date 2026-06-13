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
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
	"github.com/chromedp/chromedp"
)

// TestWorkspaceUpsert is the DOGFOOD for sty_7d7f3037 (epic:workspace-admin ·
// workspace-upsert): one document_upsert-shaped verb creates (no id) and
// patches (id present) a workspace.
//
//	create — a platform-admin creates a workspace and resolves as its admin;
//	         an executor api-key create is refused; a non-admin create is refused;
//	patch  — the owner updates the description (rendered on the workspace page,
//	         asserted via chromedp); a non-member patch is refused ErrForbidden.
func TestWorkspaceUpsert(t *testing.T) {
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
	admin, err := env.Store.GetUserByID(ctx, "usr_dev_admin")
	if err != nil {
		t.Fatalf("get dev admin: %v", err)
	}
	user, err := env.Store.GetUserByID(ctx, "usr_dev_user")
	if err != nil {
		t.Fatalf("get dev user: %v", err)
	}

	upsert := func(c context.Context, body string) (workspace.Workspace, error) {
		raw, err := verb.Dispatch(c, "workspace_upsert", json.RawMessage(body))
		if err != nil {
			return workspace.Workspace{}, err
		}
		var w workspace.Workspace
		if err := json.Unmarshal(raw, &w); err != nil {
			return workspace.Workspace{}, err
		}
		return w, nil
	}
	forbidden := func(t *testing.T, err error) {
		t.Helper()
		if err == nil || !errors.Is(err, verb.ErrForbidden) {
			t.Fatalf("expected ErrForbidden, got %v", err)
		}
	}

	// AC1/AC2: a platform-admin creates a workspace (no id) and resolves as its
	// admin (owner_user_id = caller + an 'admin' membership row).
	ws, err := upsert(authWithUser(ctx, admin), `{"name":"acme-ws","description":"initial"}`)
	if err != nil {
		t.Fatalf("admin create: %v", err)
	}
	if ws.ID == "" || ws.OwnerUserID != admin.ID {
		t.Fatalf("created workspace owner mismatch: id=%q owner=%q want %q", ws.ID, ws.OwnerUserID, admin.ID)
	}
	if ws.Description != "initial" {
		t.Fatalf("created workspace description = %q, want %q", ws.Description, "initial")
	}
	if role, err := wsStore.GetRole(ctx, ws.ID, admin.ID); err != nil || role != workspace.RoleAdmin {
		t.Fatalf("creator should resolve to admin; role=%q err=%v", role, err)
	}

	// AC2: an executor api-key create is refused (no agent-owned workspaces),
	// even when the underlying user is a platform-admin.
	t.Run("executor api-key create refused", func(t *testing.T) {
		c := auth.WithAPIKeyRole(authWithUser(ctx, admin), auth.APIKeyRoleExecutor)
		_, err := upsert(c, `{"name":"agent-ws"}`)
		forbidden(t, err)
	})

	// AC3: a non-admin human create is refused (create requires platform-admin).
	t.Run("non-admin create refused", func(t *testing.T) {
		_, err := upsert(authWithUser(ctx, user), `{"name":"user-ws"}`)
		forbidden(t, err)
	})

	// AC3: a patch by a non-member (not owner, not platform-admin) is refused.
	t.Run("non-member patch refused", func(t *testing.T) {
		_, err := upsert(authWithUser(ctx, user), `{"id":"`+ws.ID+`","description":"hijack"}`)
		forbidden(t, err)
	})

	// AC4: patch is field-merge — set description, leave name untouched.
	const newDesc = "ACME engineering workspace — patched"
	patched, err := upsert(authWithUser(ctx, admin), `{"id":"`+ws.ID+`","description":"`+newDesc+`"}`)
	if err != nil {
		t.Fatalf("owner patch: %v", err)
	}
	if patched.Description != newDesc {
		t.Fatalf("patched description = %q, want %q", patched.Description, newDesc)
	}
	if patched.Name != "acme-ws" {
		t.Fatalf("patch must leave name untouched; got %q", patched.Name)
	}

	// AC5/AC6 (chromedp): the workspace page renders the patched description.
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), chromedpHeadlessOpts()...)
	t.Cleanup(cancelAlloc)
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	t.Cleanup(cancelBrowser)
	bctx, cancelRun := context.WithTimeout(browserCtx, 60*time.Second)
	t.Cleanup(cancelRun)

	var renderedDesc string
	if err := chromedp.Run(bctx,
		chromedp.Navigate(env.ServerURL+"/login"),
		chromedp.WaitVisible(`form[data-form="login"]`, chromedp.ByQuery),
		chromedp.Click(`button[data-action="dev-login-admin"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
		chromedp.Navigate(env.ServerURL+"/workspaces/"+ws.ID),
		chromedp.WaitVisible(`[data-field="workspace-description"]`, chromedp.ByQuery),
		chromedp.Text(`[data-field="workspace-description"]`, &renderedDesc, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("workspace page render: %v", err)
	}
	if strings.TrimSpace(renderedDesc) != newDesc {
		t.Fatalf("workspace page description = %q, want %q", renderedDesc, newDesc)
	}
}
