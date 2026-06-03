// Package cliconfig loads the satellites CLI's TOML config file
// (typically ./.satellites/satellites.toml), which holds NON-secret
// config only. The api-key the CLI presents is resolved separately from
// the user-level credential store (credstore.go), provisioned by
// `satellites auth` — it is never stored in the repo-committed TOML.
//
// Resolution order for the path:
//
//  1. Explicit path (--config flag, or argument to Load).
//  2. $SATELLITES_CONFIG env var.
//  3. ./.satellites/satellites.toml (walked up from CWD).
//
// Missing file is NOT an error from Load — the caller checks
// Config.IsConfigured() and falls back to in-process dispatch when
// no server is configured.
package cliconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config is the on-disk shape persisted at .satellites/satellites.toml,
// holding NON-secret config only. The api-key is no longer stored here —
// it lives in the user-level credential store (credstore.go), resolved
// onto Token at Load time keyed by ServerURL.
type Config struct {
	ServerURL      string `toml:"server_url"`
	ProjectID      string `toml:"project_id"`
	RepoPath       string `toml:"repo_path"`
	WorktreeRoot   string `toml:"worktree_root"`
	LogPath        string `toml:"log_path"`
	BranchTemplate string `toml:"branch_template"`

	// SkillsRoot overrides where `skill sync` / `deploy` materialise
	// substrate skills. Empty means the default — <repo>/.claude/skills,
	// anchored to the directory holding .satellites/, NOT the process CWD.
	// A relative value is resolved against that repo root; absolute is used
	// verbatim.
	SkillsRoot string `toml:"skills_root"`

	// Token is the executor api-key presented on every server call.
	// Resolved at Load from the credential store (NOT the TOML), so it
	// is never a repo-committed secret. Empty until `satellites auth`.
	Token string `toml:"-"`
}

// IsConfigured reports whether the config carries enough to talk to a
// satellites-server (a URL + an api-key). Callers use this to decide
// whether to dispatch verbs remotely (HTTP) or fall back to the
// in-process registry.
func (c Config) IsConfigured() bool {
	return strings.TrimSpace(c.ServerURL) != "" && strings.TrimSpace(c.Token) != ""
}

// Load returns the resolved Config. An empty explicitPath triggers the
// env / walk-up resolution chain. A missing file returns the zero
// Config plus a typed ErrNotFound — caller decides whether to treat
// that as a failure or fall back to in-process dispatch.
func Load(explicitPath string) (Config, string, error) {
	path, err := resolvePath(explicitPath)
	if err != nil {
		return Config{}, "", err
	}
	if path == "" {
		return Config{}, "", ErrNotFound
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, path, fmt.Errorf("cliconfig: read %s: %w", path, err)
	}
	var cfg Config
	if _, err := toml.Decode(string(b), &cfg); err != nil {
		return Config{}, path, fmt.Errorf("cliconfig: parse %s: %w", path, err)
	}
	// Resolve the bearer from the user-level credential store keyed by
	// server_url (provisioned by `satellites auth`). A missing credential
	// is not an error — Token stays empty and IsConfigured reports false,
	// so the caller falls back to in-process dispatch.
	if cred, err := LoadCredential(cfg.ServerURL); err == nil {
		cfg.Token = cred.Token
	}
	// Non-interactive override (CI): SATELLITES_API_KEY presents an api-key
	// without the user-level credential store that `satellites auth` writes.
	// CI has no interactive OAuth flow — the deploy gate (sty_1ad84429) reads
	// story status against the prod server using a key injected as a repo
	// secret. A set env var wins over any stored credential; absent, behaviour
	// is unchanged. The repo-committed TOML still supplies server_url, so this
	// is the only credential CI needs to inject.
	if tok := strings.TrimSpace(os.Getenv("SATELLITES_API_KEY")); tok != "" {
		cfg.Token = tok
	}
	return cfg, path, nil
}

// ErrNotFound signals that no config file was found at the resolved
// path. Callers fall back to in-process dispatch in this case.
var ErrNotFound = errors.New("cliconfig: not found")

// StripAuthBlock removes the top-level `[auth]` table from the TOML at
// path. The api-key the CLI used to read from `[auth].token` now lives
// in the credential store (credstore.go), so a leftover `[auth]` block
// is a stale, ignored secret — `satellites auth` calls this after it
// stores the credential. Returns whether anything was removed.
//
// Line-based on purpose: it preserves the rest of the file verbatim
// (comments, key order, other tables). A future `dev mode` may
// deliberately reintroduce a TOML-token path; until then the block is
// always scrubbed.
func StripAuthBlock(path string) (bool, error) {
	if strings.TrimSpace(path) == "" {
		return false, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("cliconfig: read %s: %w", path, err)
	}

	lines := strings.Split(string(b), "\n")
	out := make([]string, 0, len(lines))
	removed, skipping := false, false
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if skipping {
			if strings.HasPrefix(t, "[") { // next table ends the [auth] block
				skipping = false
			} else {
				removed = true
				continue
			}
		}
		if t == "[auth]" {
			skipping, removed = true, true
			continue
		}
		out = append(out, ln)
	}
	if !removed {
		return false, nil
	}

	content := strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n"
	perm := os.FileMode(0o644)
	if fi, statErr := os.Stat(path); statErr == nil {
		perm = fi.Mode().Perm()
	}
	if err := os.WriteFile(path, []byte(content), perm); err != nil {
		return false, fmt.Errorf("cliconfig: write %s: %w", path, err)
	}
	return true, nil
}

func resolvePath(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if v := os.Getenv("SATELLITES_CONFIG"); v != "" {
		return v, nil
	}
	// Walk up from CWD looking for .satellites/satellites.toml.
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("cliconfig: getwd: %w", err)
	}
	dir := cwd
	for {
		candidate := filepath.Join(dir, ".satellites", "satellites.toml")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		dir = parent
	}
}
