//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestWorkspaceProjectSeedApply exercises the full filebased-seed
// substrate: store-layer ApplySeed idempotency + verb-layer round-trips
// for both workspace_seed_apply and project_seed_apply. Covers
// sty_7f4b689c AC#2 (verbs land seed_md+seed_updated_at) and AC#5
// (idempotent — same body twice = no-op).
func TestWorkspaceProjectSeedApply(t *testing.T) {
	env := testbootstrap.SetUp(t)
	testbootstrap.Reset(t, env)

	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(pjStore)
	t.Cleanup(func() {
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
	})

	ctx := context.Background()
	now := time.Date(2026, 5, 25, 10, 0, 0, 0, time.UTC)
	ws, err := wsStore.Create(ctx, "", "seed-target-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	pj, err := pjStore.Create(ctx, project.CreateInput{
		WorkspaceID: ws.ID,
		Name:        "seed-target-pj",
	}, now)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	t.Run("workspace_seed_apply first call writes + bumps seed_updated_at", func(t *testing.T) {
		req, _ := json.Marshal(verb.WorkspaceSeedApplyRequest{WorkspaceID: ws.ID, Body: "principle: prefer simplicity"})
		raw, err := verb.Dispatch(ctx, "workspace_seed_apply", req)
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		var resp verb.WorkspaceSeedApplyResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !resp.Applied {
			t.Fatalf("expected Applied=true on first call: %+v", resp)
		}
		if resp.Workspace.SeedMD != "principle: prefer simplicity" {
			t.Fatalf("seed_md mismatch: %q", resp.Workspace.SeedMD)
		}
		if resp.Workspace.SeedUpdatedAt == nil {
			t.Fatalf("expected seed_updated_at non-nil after first apply")
		}
	})

	t.Run("workspace_seed_apply same body returns no-change", func(t *testing.T) {
		// Capture the timestamp from the prior apply so we can prove
		// the no-op does not advance it.
		before, err := wsStore.GetByID(ctx, ws.ID)
		if err != nil {
			t.Fatalf("get before: %v", err)
		}
		req, _ := json.Marshal(verb.WorkspaceSeedApplyRequest{WorkspaceID: ws.ID, Body: "principle: prefer simplicity"})
		raw, err := verb.Dispatch(ctx, "workspace_seed_apply", req)
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		var resp verb.WorkspaceSeedApplyResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if resp.Applied || resp.Reason != "no change" {
			t.Fatalf("expected Applied=false reason='no change', got %+v", resp)
		}
		if !resp.Workspace.SeedUpdatedAt.Equal(*before.SeedUpdatedAt) {
			t.Fatalf("seed_updated_at advanced on no-op")
		}
	})

	t.Run("workspace_seed_apply different body advances seed_updated_at", func(t *testing.T) {
		before, _ := wsStore.GetByID(ctx, ws.ID)
		// Wait a tick to force a distinguishable timestamp; the store
		// uses time.Now().UTC() inside the verb call.
		time.Sleep(2 * time.Millisecond)
		req, _ := json.Marshal(verb.WorkspaceSeedApplyRequest{WorkspaceID: ws.ID, Body: "principle: integration tests over mocks"})
		raw, err := verb.Dispatch(ctx, "workspace_seed_apply", req)
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		var resp verb.WorkspaceSeedApplyResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !resp.Applied {
			t.Fatalf("expected Applied=true: %+v", resp)
		}
		if !resp.Workspace.SeedUpdatedAt.After(*before.SeedUpdatedAt) {
			t.Fatalf("seed_updated_at not advanced: before=%v after=%v", before.SeedUpdatedAt, resp.Workspace.SeedUpdatedAt)
		}
	})

	t.Run("workspace_seed_apply rejects unknown id", func(t *testing.T) {
		req, _ := json.Marshal(verb.WorkspaceSeedApplyRequest{WorkspaceID: "wksp_missing", Body: "x"})
		_, err := verb.Dispatch(ctx, "workspace_seed_apply", req)
		if err == nil {
			t.Fatalf("expected error for unknown workspace_id")
		}
	})

	t.Run("project_seed_apply first call writes + bumps seed_updated_at", func(t *testing.T) {
		req, _ := json.Marshal(verb.ProjectSeedApplyRequest{ProjectID: pj.ID, Body: "context: project-scope principles"})
		raw, err := verb.Dispatch(ctx, "project_seed_apply", req)
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		var resp verb.ProjectSeedApplyResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !resp.Applied {
			t.Fatalf("expected Applied=true on first call: %+v", resp)
		}
		if resp.Project.SeedMD != "context: project-scope principles" {
			t.Fatalf("seed_md mismatch: %q", resp.Project.SeedMD)
		}
		if resp.Project.SeedUpdatedAt == nil {
			t.Fatalf("expected seed_updated_at non-nil")
		}
	})

	t.Run("project_seed_apply same body returns no-change", func(t *testing.T) {
		before, _ := pjStore.GetByID(ctx, pj.ID)
		req, _ := json.Marshal(verb.ProjectSeedApplyRequest{ProjectID: pj.ID, Body: "context: project-scope principles"})
		raw, err := verb.Dispatch(ctx, "project_seed_apply", req)
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		var resp verb.ProjectSeedApplyResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if resp.Applied || resp.Reason != "no change" {
			t.Fatalf("expected no-change: %+v", resp)
		}
		if !resp.Project.SeedUpdatedAt.Equal(*before.SeedUpdatedAt) {
			t.Fatalf("seed_updated_at advanced on no-op")
		}
	})

	t.Run("project_seed_apply rejects unknown id", func(t *testing.T) {
		req, _ := json.Marshal(verb.ProjectSeedApplyRequest{ProjectID: "proj_missing", Body: "x"})
		_, err := verb.Dispatch(ctx, "project_seed_apply", req)
		if err == nil {
			t.Fatalf("expected error for unknown project_id")
		}
	})
}
