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
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestEpicTerminalChildren is the DOGFOOD for sty_d43af8dc (epic:goal-context ·
// no-children-on-done-epics): a story may not be created under — or re-parented
// into — an epic anchor that is in a terminal status (done/cancelled), while a
// reopened or non-terminal parent accepts children normally.
func TestEpicTerminalChildren(t *testing.T) {
	env := testbootstrap.SetUp(t)

	docStore := document.New(env.DB)
	authStore := auth.New(env.DB)
	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	verb.SetDocumentStore(docStore)
	verb.SetAuthStore(authStore)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(pjStore)
	t.Cleanup(func() {
		verb.SetDocumentStore(nil)
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
	})

	bg := context.Background()
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)

	if _, err := env.DB.Exec(`TRUNCATE api_keys, users, workspace_members RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate auth: %v", err)
	}
	if err := authStore.DevSeed(bg); err != nil {
		t.Fatalf("dev seed: %v", err)
	}
	admin, err := authStore.GetUserByEmail(bg, auth.DevAdminEmail)
	if err != nil {
		t.Fatalf("lookup admin: %v", err)
	}
	ws, err := wsStore.Create(bg, admin.ID, "goal-context-ws", now)
	if err != nil {
		t.Fatalf("create ws: %v", err)
	}
	pj, err := pjStore.Create(bg, project.CreateInput{WorkspaceID: ws.ID, Name: "gc", OwnerUserID: admin.ID}, now)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	ctx := authWithUser(bg, admin)

	createStory := func(name, parentID string) (string, error) {
		body := `{"type":"story","project_id":"` + pj.ID + `","workspace_id":"` + ws.ID + `","name":"` + name + `"`
		if parentID != "" {
			body += `,"parent_id":"` + parentID + `"`
		}
		body += `}`
		raw, err := verb.Dispatch(ctx, "document_upsert", json.RawMessage(body))
		if err != nil {
			return "", err
		}
		var resp struct {
			Document struct {
				ID string `json:"id"`
			} `json:"document"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return "", err
		}
		return resp.Document.ID, nil
	}
	setStatus := func(id, status string) error {
		_, err := verb.Dispatch(ctx, "document_upsert", json.RawMessage(`{"id":"`+id+`","status":"`+status+`"}`))
		return err
	}
	reparent := func(id, parentID string) error {
		_, err := verb.Dispatch(ctx, "document_upsert", json.RawMessage(`{"id":"`+id+`","parent_id":"`+parentID+`"}`))
		return err
	}
	badRequest := func(t *testing.T, err error) {
		t.Helper()
		if err == nil || !errors.Is(err, verb.ErrBadRequest) {
			t.Fatalf("expected ErrBadRequest, got %v", err)
		}
	}

	// Seed an epic anchor and drive it to done.
	anchor, err := createStory("anchor-epic", "")
	if err != nil {
		t.Fatalf("create anchor: %v", err)
	}
	if err := setStatus(anchor, "done"); err != nil {
		t.Fatalf("close anchor: %v", err)
	}

	// AC1 — creating a child under a done epic is refused.
	_, errUnderDone := createStory("child-under-done", anchor)
	badRequest(t, errUnderDone)

	// AC4 — reopen the anchor, then the same create succeeds.
	if err := setStatus(anchor, "backlog"); err != nil {
		t.Fatalf("reopen anchor: %v", err)
	}
	child, err := createStory("child-after-reopen", anchor)
	if err != nil {
		t.Fatalf("create child under reopened anchor: %v", err)
	}

	// AC2 — re-parenting an existing (backlog) story INTO a done epic is refused.
	anchorB, err := createStory("anchor-epic-b", "")
	if err != nil {
		t.Fatalf("create anchorB: %v", err)
	}
	if err := setStatus(anchorB, "done"); err != nil {
		t.Fatalf("close anchorB: %v", err)
	}
	badRequest(t, reparent(child, anchorB))

	// AC3 — re-parenting into a non-terminal epic is unaffected.
	anchorC, err := createStory("anchor-epic-c", "")
	if err != nil {
		t.Fatalf("create anchorC: %v", err)
	}
	if err := reparent(child, anchorC); err != nil {
		t.Fatalf("re-parent into a backlog epic should succeed, got %v", err)
	}
}
