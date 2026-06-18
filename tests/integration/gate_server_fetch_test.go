//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestGateDispatchResolvesFromServer is the integration-tier evidence for
// sty_b8de4776 AC1/AC3/AC4: a reviewer gate that exists ONLY on the server —
// absent from the worktree .claude/skills and not embedded — dispatches
// successfully because the dispatcher fetches its body from the server (by name,
// honouring scope precedence) and injects it into the `claude -p` run. A shim
// stands in for claude and records the system prompt it was handed, so the test
// can assert the SERVER body (and, with a project override, the most-specific
// scope's body) is what reached the gate. No local install required.
func TestGateDispatchResolvesFromServer(t *testing.T) {
	env := testbootstrap.SetUp(t)
	ctx := context.Background()
	now := time.Now().UTC()

	docStore := document.New(env.DB)
	authStore := auth.New(env.DB)
	wsStore := workspace.New(env.DB)
	verb.SetDocumentStore(docStore)
	verb.SetAuthStore(authStore)
	verb.SetWorkspaceStore(wsStore)
	t.Cleanup(func() {
		verb.SetDocumentStore(nil)
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
	})

	if _, err := env.DB.Exec(`TRUNCATE api_keys, users, workspace_members RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if err := authStore.DevSeed(ctx); err != nil {
		t.Fatalf("dev seed: %v", err)
	}
	admin, err := authStore.GetUserByEmail(ctx, auth.DevAdminEmail)
	if err != nil {
		t.Fatalf("admin: %v", err)
	}
	ctxAdmin := authWithUser(ctx, admin)

	ws, err := wsStore.Create(ctx, admin.ID, "GF", now)
	if err != nil {
		t.Fatalf("ws: %v", err)
	}
	if err := wsStore.AddMember(ctx, ws.ID, admin.ID, workspace.RoleAdmin, admin.ID, now); err != nil {
		t.Fatalf("member: %v", err)
	}
	if _, err := env.DB.Exec(`INSERT INTO projects (id, workspace_id, name) VALUES ($1,$2,'p')`, "proj_gatefetch", ws.ID); err != nil {
		t.Fatalf("project: %v", err)
	}

	const gateName = "satellites-server-only-review"
	sysBody := "---\nname: " + gateName + "\nkind: gate\ntags: [kind:gate]\n---\nSYSTEM RUBRIC MARKER\n"
	if err := document.SeedSystemTyped(ctx, docStore, document.TypeSkill, gateName, sysBody, "system:seed", now); err != nil {
		t.Fatalf("seed system gate: %v", err)
	}

	// Fetch closure backed by the real document_get inherit cascade — the
	// server-side scope resolution the prod fetcher relies on. Returns the raw
	// SKILL.md for the most-specific scope.
	fetch := func(ctx context.Context, skillName string) ([]byte, bool, error) {
		raw, derr := verb.Dispatch(ctxAdmin, "document_get", json.RawMessage(
			`{"name":"`+skillName+`","scope":"project","workspace_id":"`+ws.ID+`","project_id":"proj_gatefetch","inherit":true}`))
		if derr != nil {
			return nil, false, nil // absent in every scope → fail closed upstream
		}
		var got verb.DocumentGetResponse
		if err := json.Unmarshal(raw, &got); err != nil {
			return nil, false, err
		}
		if len(got.Versions) == 0 {
			return nil, false, nil
		}
		return []byte(got.RawBody), true, nil
	}

	// Shim claude: record the system prompt it was handed, then emit accept.
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	shim := filepath.Join(dir, "claude-shim.sh")
	script := "#!/bin/sh\nprintf '%s' \"$*\" > \"" + argsFile + "\"\nprintf '%s' '{\"decision\":\"accept\",\"notes\":\"resolved via server\"}'\n"
	if err := os.WriteFile(shim, []byte(script), 0o755); err != nil {
		t.Fatalf("write shim: %v", err)
	}

	disp := verb.ClaudeCLIGateDispatcher{BinaryPath: shim, Fetch: fetch}
	worktree := t.TempDir() // no .claude/skills — the reviewer is absent locally

	dispatchOnce := func(t *testing.T) verb.GateOutput {
		t.Helper()
		out, derr := disp.Dispatch(context.Background(), verb.GateInput{
			SkillName:    gateName,
			StoryID:      "sty_gatefetch",
			ProjectID:    "proj_gatefetch",
			WorkspaceID:  ws.ID,
			StoryStatus:  "in_progress",
			WorktreeRoot: worktree,
		})
		if derr != nil {
			t.Fatalf("dispatch: %v", derr)
		}
		return out
	}

	t.Run("system-only gate dispatches via server", func(t *testing.T) {
		out := dispatchOnce(t)
		if out.Decision != verb.GateDecisionAccept {
			t.Fatalf("decision=%q want accept", out.Decision)
		}
		injected, err := os.ReadFile(argsFile)
		if err != nil {
			t.Fatalf("read shim args: %v", err)
		}
		if !strings.Contains(string(injected), "SYSTEM RUBRIC MARKER") {
			t.Fatalf("server gate body not injected into claude run: %q", injected)
		}
	})

	t.Run("project override is the body injected (scope precedence)", func(t *testing.T) {
		projKey := document.Key{Scope: document.ScopeProject, WorkspaceID: ws.ID, ProjectID: "proj_gatefetch", Name: gateName}
		projBody := "---\nname: " + gateName + "\nkind: gate\ntags: [kind:gate]\n---\nPROJECT RUBRIC MARKER\n"
		if _, _, err := docStore.Upsert(ctx, document.UpsertInput{Key: projKey, Type: document.TypeSkill, Body: projBody}, now); err != nil {
			t.Fatalf("upsert project override: %v", err)
		}
		out := dispatchOnce(t)
		if out.Decision != verb.GateDecisionAccept {
			t.Fatalf("decision=%q want accept", out.Decision)
		}
		injected, err := os.ReadFile(argsFile)
		if err != nil {
			t.Fatalf("read shim args: %v", err)
		}
		if !strings.Contains(string(injected), "PROJECT RUBRIC MARKER") {
			t.Fatalf("most-specific (project) gate body must be injected, got: %q", injected)
		}
	})
}
