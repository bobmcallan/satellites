package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunDeploy_AbortsOnValidationViolation: deploy validates the whole
// config/ tree first and refuses (nothing uploaded, no dispatch) when a
// source breaks the inclusion rule — AC1. A skills/ file declaring
// type:document is a type-mismatch violation, caught before any server
// call, so this needs no server.
func TestRunDeploy_AbortsOnValidationViolation(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "config", "wksp_x", "proj_y", "skills", "bad.md")
	if err := os.MkdirAll(filepath.Dir(bad), 0o755); err != nil {
		t.Fatal(err)
	}
	// type:document under a skills/ dir → type-mismatch.
	body := "---\nname: bad\ntype: document\n---\nbody\n"
	if err := os.WriteFile(bad, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	var out bytes.Buffer
	err := runDeploy(context.Background(), &out, "", "", false)
	if err == nil {
		t.Fatal("expected deploy to refuse on a validation violation")
	}
	if !strings.Contains(err.Error(), "refused") {
		t.Errorf("error should say deploy refused, got %v", err)
	}
	if !strings.Contains(out.String(), "validation failed") {
		t.Errorf("output should report the validation failure:\n%s", out.String())
	}
	// The push banner must not have printed — abort is before any dispatch.
	if strings.Contains(out.String(), "push:") {
		t.Errorf("deploy must abort before pushing:\n%s", out.String())
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
