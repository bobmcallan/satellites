//go:build integration

package integration_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestProjectMemberStore covers the epic:user-admin foundation (sty_0ce67ed2):
// the project_members store mirrors the workspace member store —
// Add/List/UpdateRole/Remove/GetRole with project-role validation.
func TestProjectMemberStore(t *testing.T) {
	env := testbootstrap.SetUp(t)
	testbootstrap.Reset(t, env)

	ctx := context.Background()
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)

	authStore := auth.New(env.DB)
	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)

	u1, err := authStore.CreateUser(ctx, "usr_pm_1", "pm1@test.local", "PM One", auth.RoleUser)
	if err != nil {
		t.Fatalf("create user 1: %v", err)
	}
	u2, err := authStore.CreateUser(ctx, "usr_pm_2", "pm2@test.local", "PM Two", auth.RoleUser)
	if err != nil {
		t.Fatalf("create user 2: %v", err)
	}
	ws, err := wsStore.Create(ctx, u1.ID, "pm-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	pj, err := pjStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "pm-pj"}, now)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	t.Run("invalid role rejected", func(t *testing.T) {
		// "owner" is a workspace role, not a project role — must be rejected.
		if err := pjStore.AddMember(ctx, pj.ID, u1.ID, "owner", "", now); !errors.Is(err, project.ErrInvalidRole) {
			t.Fatalf("want ErrInvalidRole, got %v", err)
		}
	})

	t.Run("add + get role", func(t *testing.T) {
		if err := pjStore.AddMember(ctx, pj.ID, u1.ID, project.RoleWrite, "", now); err != nil {
			t.Fatalf("add member: %v", err)
		}
		role, err := pjStore.GetRole(ctx, pj.ID, u1.ID)
		if err != nil {
			t.Fatalf("get role: %v", err)
		}
		if role != project.RoleWrite {
			t.Fatalf("role = %q, want %q", role, project.RoleWrite)
		}
	})

	t.Run("get role of non-member", func(t *testing.T) {
		if _, err := pjStore.GetRole(ctx, pj.ID, u2.ID); !errors.Is(err, project.ErrMemberNotFound) {
			t.Fatalf("want ErrMemberNotFound, got %v", err)
		}
	})

	t.Run("update role", func(t *testing.T) {
		if err := pjStore.UpdateRole(ctx, pj.ID, u1.ID, project.RoleAdmin, now); err != nil {
			t.Fatalf("update role: %v", err)
		}
		role, _ := pjStore.GetRole(ctx, pj.ID, u1.ID)
		if role != project.RoleAdmin {
			t.Fatalf("role = %q, want admin", role)
		}
		// update of a non-member → ErrMemberNotFound
		if err := pjStore.UpdateRole(ctx, pj.ID, u2.ID, project.RoleRead, now); !errors.Is(err, project.ErrMemberNotFound) {
			t.Fatalf("update missing: want ErrMemberNotFound, got %v", err)
		}
	})

	t.Run("list ordered + remove", func(t *testing.T) {
		if err := pjStore.AddMember(ctx, pj.ID, u2.ID, project.RoleRead, u1.ID, now.Add(time.Hour)); err != nil {
			t.Fatalf("add member 2: %v", err)
		}
		ms, err := pjStore.ListMembers(ctx, pj.ID)
		if err != nil {
			t.Fatalf("list members: %v", err)
		}
		if len(ms) != 2 {
			t.Fatalf("len(members) = %d, want 2", len(ms))
		}
		if ms[0].UserID != u1.ID || ms[1].UserID != u2.ID {
			t.Fatalf("members not ordered oldest-first: %+v", ms)
		}
		if ms[1].AddedBy != u1.ID {
			t.Fatalf("added_by = %q, want %q", ms[1].AddedBy, u1.ID)
		}
		// remove + confirm gone; removing again → not found
		if err := pjStore.RemoveMember(ctx, pj.ID, u1.ID); err != nil {
			t.Fatalf("remove: %v", err)
		}
		if _, err := pjStore.GetRole(ctx, pj.ID, u1.ID); !errors.Is(err, project.ErrMemberNotFound) {
			t.Fatalf("after remove want ErrMemberNotFound, got %v", err)
		}
		if err := pjStore.RemoveMember(ctx, pj.ID, u1.ID); !errors.Is(err, project.ErrMemberNotFound) {
			t.Fatalf("remove missing: want ErrMemberNotFound, got %v", err)
		}
	})
}

// TestWorkspaceRoleRealignment pins the 0027 migration's data transform:
// reviewer/viewer fold to member, and every workspace ends with exactly one
// owner (the declared owner_user_id). It seeds legacy rows (bypassing the
// app validator), re-applies the idempotent up migration, and asserts the
// invariants — re-applying twice to prove idempotency.
func TestWorkspaceRoleRealignment(t *testing.T) {
	env := testbootstrap.SetUp(t)
	testbootstrap.Reset(t, env)

	ctx := context.Background()
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)

	authStore := auth.New(env.DB)
	wsStore := workspace.New(env.DB)

	creator, err := authStore.CreateUser(ctx, "usr_re_owner", "owner@re.local", "Owner", auth.RoleUser)
	if err != nil {
		t.Fatalf("create creator: %v", err)
	}
	viewerU, err := authStore.CreateUser(ctx, "usr_re_viewer", "viewer@re.local", "Viewer", auth.RoleUser)
	if err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	reviewerU, err := authStore.CreateUser(ctx, "usr_re_reviewer", "reviewer@re.local", "Reviewer", auth.RoleUser)
	if err != nil {
		t.Fatalf("create reviewer: %v", err)
	}

	// Create with the creator as declared owner — seeds creator as 'admin'.
	ws, err := wsStore.Create(ctx, creator.ID, "re-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	// Seed legacy reviewer/viewer rows directly (validator would now reject).
	if _, err := env.DB.ExecContext(ctx, `
        INSERT INTO workspace_members (workspace_id, user_id, role, added_at)
        VALUES ($1, $2, 'viewer', $3), ($1, $4, 'reviewer', $5)
    `, ws.ID, viewerU.ID, now.Add(time.Hour), reviewerU.ID, now.Add(2*time.Hour)); err != nil {
		t.Fatalf("seed legacy members: %v", err)
	}

	upSQL, err := os.ReadFile("../../internal/db/migrations/0027_project_members.up.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	apply := func() {
		if _, err := env.DB.ExecContext(ctx, string(upSQL)); err != nil {
			t.Fatalf("apply migration: %v", err)
		}
	}
	apply()
	apply() // idempotency: applying twice changes nothing

	// No reviewer/viewer rows remain.
	var legacy int
	if err := env.DB.QueryRowContext(ctx,
		`SELECT count(*) FROM workspace_members WHERE role IN ('reviewer','viewer')`).Scan(&legacy); err != nil {
		t.Fatalf("count legacy: %v", err)
	}
	if legacy != 0 {
		t.Fatalf("reviewer/viewer rows remain: %d", legacy)
	}

	// Exactly one owner, and it is the declared owner_user_id.
	var owners int
	if err := env.DB.QueryRowContext(ctx,
		`SELECT count(*) FROM workspace_members WHERE workspace_id=$1 AND role='owner'`, ws.ID).Scan(&owners); err != nil {
		t.Fatalf("count owners: %v", err)
	}
	if owners != 1 {
		t.Fatalf("owners = %d, want exactly 1", owners)
	}
	var ownerID string
	if err := env.DB.QueryRowContext(ctx,
		`SELECT user_id FROM workspace_members WHERE workspace_id=$1 AND role='owner'`, ws.ID).Scan(&ownerID); err != nil {
		t.Fatalf("owner id: %v", err)
	}
	if ownerID != creator.ID {
		t.Fatalf("owner = %q, want declared owner %q", ownerID, creator.ID)
	}

	// The folded viewer/reviewer are now members.
	var members int
	if err := env.DB.QueryRowContext(ctx,
		`SELECT count(*) FROM workspace_members WHERE workspace_id=$1 AND role='member'`, ws.ID).Scan(&members); err != nil {
		t.Fatalf("count members: %v", err)
	}
	if members != 2 {
		t.Fatalf("members = %d, want 2 (folded viewer + reviewer)", members)
	}
}
