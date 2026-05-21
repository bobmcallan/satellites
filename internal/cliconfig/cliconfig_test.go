package cliconfig

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_ExplicitPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "satellites.toml")
	body := `server_url = "https://example.com"
repo_path = "."
worktree_root = "./.satellites/worktree"
log_path = "./.satellites/logs"
branch_template = "client-{task_id}"

[auth]
token = "sk_test_abc123"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
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
	if cfg.Auth.Token != "sk_test_abc123" {
		t.Errorf("auth.token = %q", cfg.Auth.Token)
	}
	if !cfg.IsConfigured() {
		t.Error("IsConfigured should be true")
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
	path := filepath.Join(dir, "alt.toml")
	body := `server_url = "https://env.example"
[auth]
token = "sk_env"
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
		{"token only", Config{Auth: AuthBlock{Token: "x"}}, false},
		{"both", Config{ServerURL: "x", Auth: AuthBlock{Token: "y"}}, true},
		{"whitespace url", Config{ServerURL: "  ", Auth: AuthBlock{Token: "y"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.IsConfigured(); got != tc.want {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}
