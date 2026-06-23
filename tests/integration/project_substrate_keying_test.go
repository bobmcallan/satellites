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

// TestProjectSubstrateKeying is the DOGFOOD for sty_c6de961e
// (epic:goal-context · project-substrate-not-workspace-keyed): a project's
// project-scoped substrate resolves by project_id and survives a home-workspace
// move with no re-upload (AC1), and a workspace-scoped skill upsert is refused
// (AC2).
func TestProjectSubstrateKeying(t *testing.T) {
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
	admin, err := authStore.GetUserByEmail(bg, auth.DevAdminEmail) // platform-admin
	if err != nil {
		t.Fatalf("lookup admin: %v", err)
	}
	wsA, err := wsStore.Create(bg, admin.ID, "ws-a", now)
	if err != nil {
		t.Fatalf("create wsA: %v", err)
	}
	wsB, err := wsStore.Create(bg, admin.ID, "ws-b", now)
	if err != nil {
		t.Fatalf("create wsB: %v", err)
	}
	pj, err := pjStore.Create(bg, project.CreateInput{WorkspaceID: wsA.ID, Name: "keyed", OwnerUserID: admin.ID}, now)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	ctx := authWithUser(bg, admin)

	upsert := func(body string) error {
		_, err := verb.Dispatch(ctx, "document_upsert", json.RawMessage(body))
		return err
	}
	// names resolved at project scope, listing with the given workspace_id.
	names := func(t *testing.T, typ, ws string) map[string]bool {
		t.Helper()
		raw, err := verb.Dispatch(ctx, "document_list", json.RawMessage(
			`{"type":"`+typ+`","scope":"project","project_id":"`+pj.ID+`","workspace_id":"`+ws+`"}`))
		if err != nil {
			t.Fatalf("list %s: %v", typ, err)
		}
		var resp struct {
			Items []struct {
				Name string `json:"name"`
			} `json:"items"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		out := map[string]bool{}
		for _, it := range resp.Items {
			out[it.Name] = true
		}
		return out
	}

	// Seed a project skill + a project document under home workspace A. The
	// skill is a behaviour kind, so it carries a review attestation (sty_50ae9b96).
	skillReq, _ := json.Marshal(withSkillReview(map[string]any{
		"type": "skill", "scope": "project", "project_id": pj.ID,
		"workspace_id": wsA.ID, "name": "proj-skill", "body": "# proj skill",
	}))
	if err := upsert(string(skillReq)); err != nil {
		t.Fatalf("upload skill: %v", err)
	}
	if err := upsert(`{"type":"document","scope":"project","project_id":"` + pj.ID + `","workspace_id":"` + wsA.ID + `","name":"proj-doc","body":"# proj doc"}`); err != nil {
		t.Fatalf("upload doc: %v", err)
	}

	// Pre-move: both resolve when listed against home A.
	if !names(t, "skill", wsA.ID)["proj-skill"] {
		t.Fatalf("skill not resolvable before move")
	}
	if !names(t, "document", wsA.ID)["proj-doc"] {
		t.Fatalf("doc not resolvable before move")
	}

	// Move the project's home A -> B (platform-admin authority).
	if _, err := verb.Dispatch(ctx, "project_update", json.RawMessage(
		`{"id":"`+pj.ID+`","workspace_id":"`+wsB.ID+`"}`)); err != nil {
		t.Fatalf("home move: %v", err)
	}

	// AC1 — post-move, the SAME set resolves with no re-upload: against the new
	// home B, and still against the old A (workspace_id is no longer a key).
	for _, ws := range []struct{ label, id string }{{"new home B", wsB.ID}, {"old home A", wsA.ID}} {
		if !names(t, "skill", ws.id)["proj-skill"] {
			t.Errorf("skill orphaned after move (listed via %s)", ws.label)
		}
		if !names(t, "document", ws.id)["proj-doc"] {
			t.Errorf("doc orphaned after move (listed via %s)", ws.label)
		}
	}

	// AC2 — a workspace-scoped skill upsert is refused. Carry a valid review
	// attestation so the refusal is the SCOPE check (ErrBadRequest), not the
	// behaviour-review barrier (sty_50ae9b96).
	wsSkillReq, _ := json.Marshal(withSkillReview(map[string]any{
		"type": "skill", "scope": "workspace", "workspace_id": wsB.ID,
		"name": "ws-skill", "body": "# nope",
	}))
	err = upsert(string(wsSkillReq))
	if err == nil || !errors.Is(err, verb.ErrBadRequest) {
		t.Errorf("workspace-scoped skill upsert should be ErrBadRequest, got %v", err)
	}
}
