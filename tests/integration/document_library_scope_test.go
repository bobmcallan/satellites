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
)

// libProvenanceBody is the published test skill: provenance rides in the
// body frontmatter (publishing project, repo, version) — the row itself
// contributes publisher (project_id) and the version chain.
const libProvenanceBody = `---
name: deploy-checks
library:
  publisher: proj_pubA
  repo: github.com/bobmcallan/satellites
  version: 0.0.170
---

# Deploy checks

Verify the deploy pipeline before shipping.
`

// TestLibraryPublishReadAcrossProjects exercises the library scope end-to-end
// (epic:skill-library, sty_5678cc4b): a skill published into one project's
// publisher namespace is readable from a different workspace/project context
// (AC1), identity is (publisher, name) so two publishers' same-named skills
// coexist and list/get distinguish them (AC2), rows default audience='all'
// and carry their provenance intact (AC4), and the read path filters on
// audience (AC4) — all through the existing document_* verbs (AC5).
func TestLibraryPublishReadAcrossProjects(t *testing.T) {
	env := testbootstrap.SetUp(t)
	ctx := context.Background()
	now := time.Now()

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

	if _, err := env.DB.Exec(`TRUNCATE api_keys, users, workspace_members RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate auth: %v", err)
	}
	if err := authStore.DevSeed(ctx); err != nil {
		t.Fatalf("dev seed: %v", err)
	}
	admin, err := authStore.GetUserByEmail(ctx, auth.DevAdminEmail)
	if err != nil {
		t.Fatalf("lookup admin: %v", err)
	}
	user, err := authStore.GetUserByEmail(ctx, auth.DevUserEmail)
	if err != nil {
		t.Fatalf("lookup user: %v", err)
	}
	ctxAdmin := authWithUser(ctx, admin)
	ctxUser := authWithUser(ctx, user)

	// Publisher side: workspace A + project A, owned by admin only — the
	// consumer (user) has NO grant path into it.
	wsA, err := wsStore.Create(ctx, admin.ID, "lib-pub-ws", now)
	if err != nil {
		t.Fatalf("create wsA: %v", err)
	}
	if err := wsStore.AddMember(ctx, wsA.ID, admin.ID, workspace.RoleAdmin, admin.ID, now); err != nil {
		t.Fatalf("add admin to wsA: %v", err)
	}
	if _, err := env.DB.Exec(`INSERT INTO projects (id, workspace_id, name) VALUES ('proj_pubA', $1, 'publisher-a')`, wsA.ID); err != nil {
		t.Fatalf("seed proj_pubA: %v", err)
	}

	// Consumer side: a different workspace + project, where the non-admin
	// user holds write — their own publisher namespace.
	wsB, err := wsStore.Create(ctx, admin.ID, "lib-con-ws", now)
	if err != nil {
		t.Fatalf("create wsB: %v", err)
	}
	if err := wsStore.AddMember(ctx, wsB.ID, user.ID, workspace.RoleMember, admin.ID, now); err != nil {
		t.Fatalf("add user to wsB: %v", err)
	}
	if _, err := env.DB.Exec(`INSERT INTO projects (id, workspace_id, name) VALUES ('proj_pubB', $1, 'publisher-b')`, wsB.ID); err != nil {
		t.Fatalf("seed proj_pubB: %v", err)
	}
	if err := pjStore.AddMember(ctx, "proj_pubB", user.ID, project.RoleWrite, admin.ID, now); err != nil {
		t.Fatalf("grant user write on proj_pubB: %v", err)
	}

	upsertLibrary := func(t *testing.T, ctx context.Context, projectID, body string) (json.RawMessage, error) {
		t.Helper()
		req, _ := json.Marshal(map[string]any{
			"type": "skill", "scope": "library",
			"project_id": projectID, "name": "deploy-checks", "body": body,
		})
		return verb.Dispatch(ctx, "document_upsert", json.RawMessage(req))
	}

	// Publish into A's namespace (admin holds write on proj_pubA).
	if _, err := upsertLibrary(t, ctxAdmin, "proj_pubA", libProvenanceBody); err != nil {
		t.Fatalf("publish into proj_pubA: %v", err)
	}

	t.Run("readable across the workspace boundary with provenance intact", func(t *testing.T) {
		// user belongs to wsB only — a context entirely outside the
		// publisher's workspace/project.
		raw, err := verb.Dispatch(ctxUser, "document_get", json.RawMessage(
			`{"name":"deploy-checks","scope":"library","project_id":"proj_pubA"}`))
		if err != nil {
			t.Fatalf("cross-workspace library get: %v", err)
		}
		var got verb.DocumentGetResponse
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if got.ResolvedScope != "library" {
			t.Fatalf("resolved_scope=%s want library", got.ResolvedScope)
		}
		if got.Document.ProjectID != "proj_pubA" {
			t.Fatalf("publisher=%s want proj_pubA", got.Document.ProjectID)
		}
		if got.Document.Audience != "all" {
			t.Fatalf("audience=%q want default \"all\"", got.Document.Audience)
		}
		for _, want := range []string{"publisher: proj_pubA", "repo: github.com/bobmcallan/satellites", "version: 0.0.170"} {
			if !strings.Contains(got.RawBody, want) {
				t.Fatalf("provenance %q missing from published body:\n%s", want, got.RawBody)
			}
		}
	})

	t.Run("same name under two publishers coexists; list distinguishes by publisher", func(t *testing.T) {
		// user publishes the same skill name into their OWN namespace (B).
		if _, err := upsertLibrary(t, ctxUser, "proj_pubB", "# B's deploy checks\n"); err != nil {
			t.Fatalf("publish into proj_pubB: %v", err)
		}
		raw, err := verb.Dispatch(ctxUser, "document_list", json.RawMessage(
			`{"type":"skill","scope":"library","name_prefix":"deploy-checks"}`))
		if err != nil {
			t.Fatalf("library list: %v", err)
		}
		var resp verb.DocumentListResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatal(err)
		}
		publishers := map[string]bool{}
		for _, it := range resp.Items {
			if it.Name == "deploy-checks" && it.Scope == document.ScopeLibrary {
				publishers[it.ProjectID] = true
			}
		}
		if !publishers["proj_pubA"] || !publishers["proj_pubB"] || len(publishers) != 2 {
			t.Fatalf("library list publishers=%v want exactly {proj_pubA, proj_pubB}", publishers)
		}
		// A publisher-bounded list narrows to that namespace.
		raw, err = verb.Dispatch(ctxUser, "document_list", json.RawMessage(
			`{"type":"skill","scope":"library","project_id":"proj_pubB"}`))
		if err != nil {
			t.Fatalf("publisher-bounded list: %v", err)
		}
		resp = verb.DocumentListResponse{}
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatal(err)
		}
		for _, it := range resp.Items {
			if it.ProjectID != "proj_pubB" {
				t.Fatalf("bounded list leaked row from publisher %s", it.ProjectID)
			}
		}
	})

	t.Run("republish advances the version chain", func(t *testing.T) {
		if _, err := upsertLibrary(t, ctxAdmin, "proj_pubA", libProvenanceBody+"\nUpdated.\n"); err != nil {
			t.Fatalf("republish: %v", err)
		}
		raw, err := verb.Dispatch(ctxUser, "document_get", json.RawMessage(
			`{"name":"deploy-checks","scope":"library","project_id":"proj_pubA"}`))
		if err != nil {
			t.Fatalf("get after republish: %v", err)
		}
		var got verb.DocumentGetResponse
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if got.Document.LatestVersion != 2 {
			t.Fatalf("latest_version=%d want 2", got.Document.LatestVersion)
		}
	})

	t.Run("read path filters on audience", func(t *testing.T) {
		// No write surface for audience ships with the initial library (no
		// grant model by design), so restrict the row directly.
		if _, err := env.DB.Exec(`UPDATE documents SET audience='publisher-only' WHERE scope='library' AND project_id='proj_pubA' AND name='deploy-checks'`); err != nil {
			t.Fatalf("restrict audience: %v", err)
		}
		t.Cleanup(func() {
			_, _ = env.DB.Exec(`UPDATE documents SET audience='all' WHERE scope='library' AND project_id='proj_pubA' AND name='deploy-checks'`)
		})
		// An outsider's get is refused...
		_, err := verb.Dispatch(ctxUser, "document_get", json.RawMessage(
			`{"name":"deploy-checks","scope":"library","project_id":"proj_pubA"}`))
		if !errors.Is(err, verb.ErrForbidden) {
			t.Fatalf("restricted-audience get = %v; want ErrForbidden", err)
		}
		// ...and the row drops out of the public listing.
		raw, err := verb.Dispatch(ctxUser, "document_list", json.RawMessage(
			`{"type":"skill","scope":"library","project_id":"proj_pubA"}`))
		if err != nil {
			t.Fatalf("list after restrict: %v", err)
		}
		var resp verb.DocumentListResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatal(err)
		}
		if len(resp.Items) != 0 {
			t.Fatalf("restricted row still listed: %+v", resp.Items)
		}
		// The publisher itself still reads it (≥ read on proj_pubA).
		if _, err := verb.Dispatch(ctxAdmin, "document_get", json.RawMessage(
			`{"name":"deploy-checks","scope":"library","project_id":"proj_pubA"}`)); err != nil {
			t.Fatalf("publisher get of restricted row: %v", err)
		}
	})
}

// TestLibraryCrossNamespaceWriteRefused pins AC3: a caller without write on
// the publisher project cannot write into that namespace — the upsert is
// refused with a clear forbidden error naming the publisher confinement.
func TestLibraryCrossNamespaceWriteRefused(t *testing.T) {
	env := testbootstrap.SetUp(t)
	ctx := context.Background()
	now := time.Now()

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

	if _, err := env.DB.Exec(`TRUNCATE api_keys, users, workspace_members RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate auth: %v", err)
	}
	if err := authStore.DevSeed(ctx); err != nil {
		t.Fatalf("dev seed: %v", err)
	}
	admin, err := authStore.GetUserByEmail(ctx, auth.DevAdminEmail)
	if err != nil {
		t.Fatalf("lookup admin: %v", err)
	}
	user, err := authStore.GetUserByEmail(ctx, auth.DevUserEmail)
	if err != nil {
		t.Fatalf("lookup user: %v", err)
	}
	ctxUser := authWithUser(ctx, user)

	wsA, err := wsStore.Create(ctx, admin.ID, "lib-refuse-ws", now)
	if err != nil {
		t.Fatalf("create wsA: %v", err)
	}
	if _, err := env.DB.Exec(`INSERT INTO projects (id, workspace_id, name) VALUES ('proj_refuseA', $1, 'publisher-a')`, wsA.ID); err != nil {
		t.Fatalf("seed proj_refuseA: %v", err)
	}
	// user holds READ (not write) on the publisher project — even a reader
	// of the project may not publish into its namespace.
	if err := pjStore.AddMember(ctx, "proj_refuseA", user.ID, project.RoleRead, admin.ID, now); err != nil {
		t.Fatalf("grant user read on proj_refuseA: %v", err)
	}

	attempt := func(projectID string) error {
		req, _ := json.Marshal(map[string]any{
			"type": "skill", "scope": "library",
			"project_id": projectID, "name": "deploy-checks", "body": "# intruder\n",
		})
		_, err := verb.Dispatch(ctxUser, "document_upsert", json.RawMessage(req))
		return err
	}

	// No grant at all.
	if err := attempt("proj_pub_other"); !errors.Is(err, verb.ErrForbidden) {
		t.Fatalf("no-grant cross-namespace write = %v; want ErrForbidden", err)
	}
	// Read-only grant is still below the write bar.
	err = attempt("proj_refuseA")
	if !errors.Is(err, verb.ErrForbidden) {
		t.Fatalf("read-grant cross-namespace write = %v; want ErrForbidden", err)
	}
	if !strings.Contains(err.Error(), "publisher namespace") {
		t.Fatalf("refusal does not name the publisher confinement: %v", err)
	}
	// And a library write without a publisher is a bad request, not a panic.
	if err := attempt(""); !errors.Is(err, verb.ErrBadRequest) {
		t.Fatalf("library write without project_id = %v; want ErrBadRequest", err)
	}
}
