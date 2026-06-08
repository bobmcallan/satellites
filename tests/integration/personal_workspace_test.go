//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestEnsurePersonalWorkspace covers epic:user-admin sty_3a54bf42: the
// get-or-create personal-workspace resolver is idempotent and owns the user.
func TestEnsurePersonalWorkspace(t *testing.T) {
	env := testbootstrap.SetUp(t)
	testbootstrap.Reset(t, env)

	ctx := context.Background()
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	authStore := auth.New(env.DB)
	wsStore := workspace.New(env.DB)

	u, err := authStore.CreateUser(ctx, "usr_pw_e", "e@pw.local", "Eve", auth.RoleUser)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// none yet
	if _, err := wsStore.GetPersonalForUser(ctx, u.ID); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}

	w1, err := wsStore.EnsurePersonalWorkspace(ctx, u.ID, "Eve", now)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if w1.OwnerUserID != u.ID {
		t.Fatalf("owner = %q, want %q", w1.OwnerUserID, u.ID)
	}
	// idempotent — second call returns the same workspace, no duplicate
	w2, err := wsStore.EnsurePersonalWorkspace(ctx, u.ID, "Eve", now.Add(time.Hour))
	if err != nil {
		t.Fatalf("ensure again: %v", err)
	}
	if w2.ID != w1.ID {
		t.Fatalf("second ensure minted a new workspace: %q != %q", w2.ID, w1.ID)
	}
	got, err := wsStore.GetPersonalForUser(ctx, u.ID)
	if err != nil || got.ID != w1.ID {
		t.Fatalf("resolve personal: id=%q err=%v", got.ID, err)
	}
	// owner membership row exists at role owner
	role, err := wsStore.GetRole(ctx, w1.ID, u.ID)
	if err != nil || role != workspace.RoleOwner {
		t.Fatalf("owner membership role=%q err=%v", role, err)
	}
}

// TestPersonalWorkspaceMigration pins migration 0028: the shared default is
// repurposed in place as the oldest global admin's personal workspace
// (projects keep their workspace_id), and every other user gets a personal
// workspace. Re-applied twice to prove idempotency.
func TestPersonalWorkspaceMigration(t *testing.T) {
	env := testbootstrap.SetUp(t)
	testbootstrap.Reset(t, env)

	ctx := context.Background()
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	authStore := auth.New(env.DB)
	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)

	admin, err := authStore.CreateUser(ctx, "usr_pw_admin", "admin@pw.local", "Admin", auth.RoleAdmin)
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	plain, err := authStore.CreateUser(ctx, "usr_pw_plain", "plain@pw.local", "Plain", auth.RoleUser)
	if err != nil {
		t.Fatalf("create plain: %v", err)
	}

	// Seed a shared default workspace (NULL owner) + a project inside it.
	if _, err := env.DB.ExecContext(ctx, `
        INSERT INTO workspaces (id, name, owner_user_id, status, is_default, is_personal, created_at, updated_at)
        VALUES ('wksp_pw_def', 'default', NULL, 'active', TRUE, FALSE, $1, $1)
    `, now); err != nil {
		t.Fatalf("seed default workspace: %v", err)
	}
	pj, err := pjStore.Create(ctx, project.CreateInput{WorkspaceID: "wksp_pw_def", Name: "pw-proj"}, now)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	upSQL, err := os.ReadFile("../../internal/db/migrations/0028_personal_workspaces.up.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	apply := func() {
		if _, err := env.DB.ExecContext(ctx, string(upSQL)); err != nil {
			t.Fatalf("apply migration: %v", err)
		}
	}
	apply()
	apply() // idempotency

	// The default is repurposed as the admin's personal workspace, in place.
	var (
		isDefault, isPersonal bool
		owner                 string
	)
	if err := env.DB.QueryRowContext(ctx,
		`SELECT is_default, is_personal, COALESCE(owner_user_id,'') FROM workspaces WHERE id='wksp_pw_def'`,
	).Scan(&isDefault, &isPersonal, &owner); err != nil {
		t.Fatalf("read repurposed default: %v", err)
	}
	if isDefault {
		t.Fatal("default flag not cleared — shared default not retired")
	}
	if !isPersonal || owner != admin.ID {
		t.Fatalf("default not repurposed to admin personal: is_personal=%v owner=%q want %q", isPersonal, owner, admin.ID)
	}

	// The project did NOT move — its workspace_id is unchanged.
	got, err := pjStore.GetByID(ctx, pj.ID)
	if err != nil {
		t.Fatalf("get project: %v", err)
	}
	if got.WorkspaceID != "wksp_pw_def" {
		t.Fatalf("project re-homed: workspace_id=%q, want wksp_pw_def", got.WorkspaceID)
	}

	// The admin resolves the repurposed workspace as theirs.
	aw, err := wsStore.GetPersonalForUser(ctx, admin.ID)
	if err != nil || aw.ID != "wksp_pw_def" {
		t.Fatalf("admin personal: id=%q err=%v", aw.ID, err)
	}
	// The non-admin got a minted personal workspace (exactly one).
	pw, err := wsStore.GetPersonalForUser(ctx, plain.ID)
	if err != nil {
		t.Fatalf("plain personal: %v", err)
	}
	if pw.ID == "wksp_pw_def" {
		t.Fatal("plain user wrongly resolved the admin's workspace")
	}
}

// TestProjectCreateUsesPersonalWorkspace pins that project_create with no
// workspace_id lands the project in the caller's personal workspace (the
// retired-default replacement).
func TestProjectCreateUsesPersonalWorkspace(t *testing.T) {
	env := testbootstrap.SetUp(t)
	testbootstrap.Reset(t, env)

	ctx := context.Background()
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	authStore := auth.New(env.DB)
	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(pjStore)
	t.Cleanup(func() {
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
	})

	u, err := authStore.CreateUser(ctx, "usr_pw_c", "c@pw.local", "Cara", auth.RoleUser)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	personal, err := wsStore.EnsurePersonalWorkspace(ctx, u.ID, "Cara", now)
	if err != nil {
		t.Fatalf("ensure personal: %v", err)
	}

	raw, err := verb.Dispatch(authWithUser(ctx, u), "project_create",
		json.RawMessage(`{"name":"cara-proj"}`))
	if err != nil {
		t.Fatalf("project_create: %v", err)
	}
	var p project.Project
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.WorkspaceID != personal.ID {
		t.Fatalf("project landed in %q, want personal %q", p.WorkspaceID, personal.ID)
	}
}

// TestDevSeedPersonalWorkspaces pins AC#5: devseed still yields a working
// admin/user, each owning a personal workspace.
func TestDevSeedPersonalWorkspaces(t *testing.T) {
	env := testbootstrap.SetUp(t)
	testbootstrap.Reset(t, env)

	ctx := context.Background()
	authStore := auth.New(env.DB)
	if err := authStore.DevSeed(ctx); err != nil {
		t.Fatalf("dev seed: %v", err)
	}
	// idempotent re-seed must not duplicate
	if err := authStore.DevSeed(ctx); err != nil {
		t.Fatalf("dev seed (2nd): %v", err)
	}

	wsStore := workspace.New(env.DB)
	for _, id := range []string{"usr_dev_admin", "usr_dev_user"} {
		w, err := wsStore.GetPersonalForUser(ctx, id)
		if err != nil {
			t.Fatalf("personal workspace for %s: %v", id, err)
		}
		if w.OwnerUserID != id {
			t.Fatalf("personal workspace owner = %q, want %q", w.OwnerUserID, id)
		}
	}
}
