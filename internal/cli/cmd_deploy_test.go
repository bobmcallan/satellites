package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunDeploy_PullOnly_NoPush: deploy is pull-only. It must never emit the
// push banner nor touch config/ — pushing sources is a separate client verb
// (operator decision on the sty_be65b4dd push report). With no project_id it
// stops at scope resolution; the point is that no push happens on the way
// there and the validate-config tree is never read.
func TestRunDeploy_PullOnly_NoPush(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg")) // isolate cred store
	// A config/ tree that WOULD fail validation if deploy still pushed.
	bad := filepath.Join(dir, "config", "wksp_x", "proj_y", "skills", "bad.md")
	if err := os.MkdirAll(filepath.Dir(bad), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("---\nname: bad\ntype: document\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	var out bytes.Buffer
	err := runDeploy(context.Background(), &out, "", "", false)
	// No project_id configured → deploy stops at scope resolution, NOT at a
	// validation refusal.
	if err == nil || !strings.Contains(err.Error(), "project_id") {
		t.Fatalf("expected a project_id error, got %v", err)
	}
	if strings.Contains(out.String(), "push:") {
		t.Errorf("deploy must not push:\n%s", out.String())
	}
	if strings.Contains(out.String(), "validation failed") {
		t.Errorf("deploy must not validate config/:\n%s", out.String())
	}
}

// TestResolveDeployScope_NoProject: deploy needs a project_id to know
// which project's skills to reconcile; absent one it errors with a clear
// pointer rather than dispatching.
func TestResolveDeployScope_NoProject(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg")) // isolate cred store
	tomlPath := filepath.Join(dir, "satellites.toml")
	if err := os.WriteFile(tomlPath, []byte("server_url = \"https://x.example\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := resolveDeployScope(context.Background(), tomlPath, ""); err == nil {
		t.Fatal("expected an error when project_id is absent")
	} else if !strings.Contains(err.Error(), "project_id") {
		t.Errorf("error should mention project_id, got %v", err)
	}
}
