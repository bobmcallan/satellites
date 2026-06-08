package cliconfig

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveWorkDir(t *testing.T) {
	// Default: empty work_dir → <repo>/.satellites/work (repo-relative).
	if got, want := (Config{}).ResolveWorkDir("/repo"), filepath.Join("/repo", ".satellites", "work"); got != want {
		t.Errorf("default work dir = %q, want %q", got, want)
	}
	// Relative override resolves against the repo root.
	if got, want := (Config{WorkDir: "var/engage"}).ResolveWorkDir("/repo"), filepath.Join("/repo", "var", "engage"); got != want {
		t.Errorf("relative work dir = %q, want %q", got, want)
	}
	// Absolute override is used verbatim.
	if got := (Config{WorkDir: "/abs/work"}).ResolveWorkDir("/repo"); got != "/abs/work" {
		t.Errorf("absolute work dir = %q, want /abs/work", got)
	}
}

func TestResolveDataDirStores(t *testing.T) {
	// Default: state.db + index.db share <repo>/.satellites, beside each other —
	// state.db is NOT under work/ any more.
	if got, want := (Config{}).ResolveDataDir("/repo"), filepath.Join("/repo", ".satellites"); got != want {
		t.Errorf("default data dir = %q, want %q", got, want)
	}
	if got, want := (Config{}).ResolveStateDB("/repo"), filepath.Join("/repo", ".satellites", "state.db"); got != want {
		t.Errorf("default state.db = %q, want %q (must not be under work/)", got, want)
	}
	if got, want := (Config{}).ResolveIndexDB("/repo"), filepath.Join("/repo", ".satellites", "index.db"); got != want {
		t.Errorf("default index.db = %q, want %q", got, want)
	}

	// A data_dir override relocates BOTH stores together.
	c := Config{DataDir: "var/sat"}
	if got, want := c.ResolveStateDB("/repo"), filepath.Join("/repo", "var", "sat", "state.db"); got != want {
		t.Errorf("data_dir state.db = %q, want %q", got, want)
	}
	if got, want := c.ResolveIndexDB("/repo"), filepath.Join("/repo", "var", "sat", "index.db"); got != want {
		t.Errorf("data_dir index.db = %q, want %q", got, want)
	}
	// Absolute data_dir is used verbatim.
	if got, want := (Config{DataDir: "/abs/data"}).ResolveStateDB("/repo"), filepath.Join("/abs/data", "state.db"); got != want {
		t.Errorf("absolute data_dir state.db = %q, want %q", got, want)
	}

	// An explicit state_db override still wins over data_dir (the escape hatch).
	if got, want := (Config{DataDir: "/abs/data", StateDB: "custom/s.db"}).ResolveStateDB("/repo"), filepath.Join("/repo", "custom", "s.db"); got != want {
		t.Errorf("state_db override = %q, want %q", got, want)
	}
}

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

func TestStripAuthBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "satellites.toml")
	body := `# header comment
server_url = "https://example.com"
project_id = "proj_x"

[auth]
# the stale token
token = "sk_stale_should_be_removed"

[other]
keep = "yes"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	removed, err := StripAuthBlock(path)
	if err != nil {
		t.Fatalf("StripAuthBlock: %v", err)
	}
	if !removed {
		t.Fatal("expected removed=true")
	}
	out, _ := os.ReadFile(path)
	s := string(out)
	if strings.Contains(s, "[auth]") || strings.Contains(s, "sk_stale_should_be_removed") {
		t.Errorf("[auth]/token not scrubbed:\n%s", s)
	}
	// Non-auth content preserved verbatim.
	for _, want := range []string{"# header comment", `server_url = "https://example.com"`, `project_id = "proj_x"`, "[other]", `keep = "yes"`} {
		if !strings.Contains(s, want) {
			t.Errorf("scrub dropped %q:\n%s", want, s)
		}
	}
	// Idempotent: a second scrub is a no-op.
	if removed2, _ := StripAuthBlock(path); removed2 {
		t.Error("second StripAuthBlock should be a no-op")
	}
	// Missing file is a quiet no-op.
	if removed3, err := StripAuthBlock(filepath.Join(dir, "nope.toml")); err != nil || removed3 {
		t.Errorf("missing file: removed=%v err=%v", removed3, err)
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
