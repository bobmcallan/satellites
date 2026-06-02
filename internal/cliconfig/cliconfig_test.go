package cliconfig

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_ExplicitPath(t *testing.T) {
	dir := t.TempDir()
	// Isolate the credential store under a temp XDG dir.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg"))

	path := filepath.Join(dir, "satellites.toml")
	body := `server_url = "https://example.com"
project_id = "proj_x"
repo_path = "."
worktree_root = "./.satellites/worktree"
log_path = "./.satellites/logs"
branch_template = "client-{task_id}"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	// Before `satellites auth`: non-secret config parses, but no token is
	// resolved (the TOML carries no secret), so the client is not configured.
	cfg, got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != path {
		t.Errorf("path = %q, want %q", got, path)
	}
	if cfg.ServerURL != "https://example.com" {
		t.Errorf("server_url = %q", cfg.ServerURL)
	}
	if cfg.Token != "" {
		t.Errorf("token should be empty before auth, got %q", cfg.Token)
	}
	if cfg.IsConfigured() {
		t.Error("IsConfigured should be false without a stored credential")
	}

	// After `satellites auth` stores a credential for this server, Load
	// resolves the token from the credential store.
	if err := SaveCredential(Credential{ServerURL: "https://example.com", Token: "sk_test_abc123", Role: "executor"}); err != nil {
		t.Fatal(err)
	}
	cfg, _, err = Load(path)
	if err != nil {
		t.Fatalf("Load after auth: %v", err)
	}
	if cfg.Token != "sk_test_abc123" {
		t.Errorf("resolved token = %q", cfg.Token)
	}
	if !cfg.IsConfigured() {
		t.Error("IsConfigured should be true once a credential is stored")
	}
}

func TestLoad_Missing(t *testing.T) {
	t.Setenv("SATELLITES_CONFIG", "")
	dir := t.TempDir()
	// Run from a temp dir that has no .satellites/ on the walk up.
	t.Chdir(dir)
	_, _, err := Load("")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestLoad_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg"))
	path := filepath.Join(dir, "alt.toml")
	body := `server_url = "https://env.example"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SATELLITES_CONFIG", path)
	cfg, got, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != path {
		t.Errorf("path = %q, want %q", got, path)
	}
	if cfg.ServerURL != "https://env.example" {
		t.Errorf("server_url = %q", cfg.ServerURL)
	}
}

func TestIsConfigured(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want bool
	}{
		{"empty", Config{}, false},
		{"url only", Config{ServerURL: "x"}, false},
		{"token only", Config{Token: "x"}, false},
		{"both", Config{ServerURL: "x", Token: "y"}, true},
		{"whitespace url", Config{ServerURL: "  ", Token: "y"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.IsConfigured(); got != tc.want {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}
