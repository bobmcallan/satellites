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
)

// TestProjectTypeAdmin is the DOGFOOD for sty_e5dbde6d (epic:workspace-admin ·
// project-type-admin): a project's freeform `type` is admin-settable via the
// project patch (field-merge), readable via the read path + portal, with
// explicit-empty clear distinct from not-supplied.
func TestProjectTypeAdmin(t *testing.T) {
	// The project page's server-render of the type ([data-field="project-type"]) is covered
	// deterministically, browser-free, by internal/server.TestProjectDetailRendersType — the
	// flaky dev-login→navigate chromedp assertion (a session race, not a render bug) was
	// removed here per sty_57acbe4e. This test keeps the deterministic verb-level behaviour.
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
	wsAdmin, _ := env.Store.CreateUser(ctx, "usr_ty_admin", "tya@ty.local", "TyAdmin", "user")
	outsider, _ := env.Store.CreateUser(ctx, "usr_ty_out", "out@ty.local", "Outsider", "user")

	ws, _ := wsStore.Create(ctx, wsAdmin.ID, "type-ws", now) // wsAdmin → admin member
	pj, _ := pjStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "typed", OwnerUserID: wsAdmin.ID}, now)

	patch := func(c context.Context, body string) (project.Project, error) {
		raw, err := verb.Dispatch(c, "project_update", json.RawMessage(body))
		if err != nil {
			return project.Project{}, err
		}
		var p project.Project
		if err := json.Unmarshal(raw, &p); err != nil {
			return project.Project{}, err
		}
		return p, nil
	}
	readType := func(t *testing.T, c context.Context) string {
		t.Helper()
		raw, err := verb.Dispatch(c, "project_get", json.RawMessage(`{"id":"`+pj.ID+`"}`))
		if err != nil {
			t.Fatalf("project_get: %v", err)
		}
		var p project.Project
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatalf("decode project_get: %v", err)
		}
		return p.Type
	}

	// AC1/AC4: a workspace-admin sets a freeform type via the patch; it reads
	// back through the project read path.
	if p, err := patch(authWithUser(ctx, wsAdmin), `{"id":"`+pj.ID+`","type":"reference-corpus"}`); err != nil || p.Type != "reference-corpus" {
		t.Fatalf("admin set type: type=%q err=%v", p.Type, err)
	}
	if got := readType(t, authWithUser(ctx, wsAdmin)); got != "reference-corpus" {
		t.Fatalf("read-back type = %q, want reference-corpus", got)
	}

	// AC1 (field-merge): patching another field (description) leaves type intact.
	if _, err := patch(authWithUser(ctx, gAdmin), `{"id":"`+pj.ID+`","description":"touched"}`); err != nil {
		t.Fatalf("description patch: %v", err)
	}
	if got := readType(t, authWithUser(ctx, gAdmin)); got != "reference-corpus" {
		t.Fatalf("type changed by an unrelated patch: %q", got)
	}

	// AC3: a non-admin (not a member of the home workspace) patch is refused.
	t.Run("non-admin patch refused", func(t *testing.T) {
		_, err := patch(authWithUser(ctx, outsider), `{"id":"`+pj.ID+`","type":"hijack"}`)
		if err == nil || !errors.Is(err, verb.ErrForbidden) {
			t.Fatalf("expected ErrForbidden, got %v", err)
		}
		if got := readType(t, authWithUser(ctx, gAdmin)); got != "reference-corpus" {
			t.Fatalf("non-admin patch mutated type: %q", got)
		}
	})

	// AC2: explicit empty clears the type (distinct from not-supplied above).
	if p, err := patch(authWithUser(ctx, wsAdmin), `{"id":"`+pj.ID+`","type":""}`); err != nil || p.Type != "" {
		t.Fatalf("clear type: type=%q err=%v", p.Type, err)
	}
	if got := readType(t, authWithUser(ctx, wsAdmin)); got != "" {
		t.Fatalf("type not cleared: %q", got)
	}
}
