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

// TestProjectCreateResolvesPersonalWorkspace pins sty_5d7e6baf AC2: creating a
// project does NOT require the caller to supply a workspace_id — project_create
// resolves the caller's PERSONAL workspace when none is given — and a project
// MOVES workspaces via SetWorkspace with a stable id (its proj_… id is the
// identity, the workspace is a re-pointable home). This is the existing verb
// design; the test records it so the "project should not require the workspace"
// invariant can't silently regress. No nullable-workspace migration is needed —
// the workspace-keyed access model is preserved.
func TestProjectCreateResolvesPersonalWorkspace(t *testing.T) {
	env := testbootstrap.SetUp(t)

	authStore := auth.New(env.DB)
	wsStore := workspace.New(env.DB)
	projStore := project.New(env.DB)
	verb.SetProjectStore(projStore)
	verb.SetWorkspaceStore(wsStore)
	verb.SetAuthStore(nil) // CLI-local caller: no auth wiring → the admin gate bypasses.
	t.Cleanup(func() {
		verb.SetProjectStore(nil)
		verb.SetWorkspaceStore(nil)
	})

	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)

	u, err := authStore.CreateUser(ctx, "usr_first_touch", "ft@example.com", "First Touch", auth.RoleUser)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	personal, err := wsStore.EnsurePersonalWorkspace(ctx, u.ID, "Personal", now)
	if err != nil {
		t.Fatalf("ensure personal workspace: %v", err)
	}

	// Caller identity in ctx (what callerUserID resolves "my workspace" from),
	// the same way auth.Middleware stamps it on the live path.
	cctx := authWithUser(ctx, &auth.User{ID: u.ID})

	// project_create with NO workspace_id → resolves the caller's personal one.
	req, _ := json.Marshal(verb.ProjectCreateRequest{Name: "first-touch-project"})
	raw, err := verb.Dispatch(cctx, "project_create", req)
	if err != nil {
		t.Fatalf("project_create (no workspace_id): %v", err)
	}
	var p project.Project
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("decode project: %v", err)
	}
	if p.ID == "" {
		t.Fatal("project_create returned no id")
	}
	if p.WorkspaceID != personal.ID {
		t.Errorf("no-workspace project_create must resolve the caller's personal workspace; got %q want %q", p.WorkspaceID, personal.ID)
	}

	// The project moves workspaces with a STABLE id (id is identity, workspace
	// is a re-pointable home).
	other, err := wsStore.Create(ctx, "", "second-workspace", now)
	if err != nil {
		t.Fatalf("create second workspace: %v", err)
	}
	moved, err := projStore.SetWorkspace(ctx, p.ID, other.ID, now)
	if err != nil {
		t.Fatalf("set workspace: %v", err)
	}
	if moved.ID != p.ID {
		t.Errorf("project id must be stable across a workspace move: %q != %q", moved.ID, p.ID)
	}
	if moved.WorkspaceID != other.ID {
		t.Errorf("SetWorkspace must move the project: workspace = %q want %q", moved.WorkspaceID, other.ID)
	}
}
