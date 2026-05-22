// Package cliconfig loads the satellites CLI's TOML config file
// (typically ./.satellites/satellites.toml). The schema mirrors the
// install schema markdown's `default_config` block plus an `[auth]`
// section carrying the api-key the CLI presents on every server call.
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

// Config is the on-disk shape persisted at .satellites/satellites.toml.
// Keys mirror the install schema's `default_config` block plus a
// dedicated [auth] section for the api-key.
type Config struct {
	ServerURL      string    `toml:"server_url"`
	ProjectID      string    `toml:"project_id"`
	RepoPath       string    `toml:"repo_path"`
	WorktreeRoot   string    `toml:"worktree_root"`
	LogPath        string    `toml:"log_path"`
	BranchTemplate string    `toml:"branch_template"`
	Auth           AuthBlock `toml:"auth"`
}

// AuthBlock carries the api-key the CLI sends on every server call.
type AuthBlock struct {
	Token string `toml:"token"`
}

// IsConfigured reports whether the config carries enough to talk to a
// satellites-server (a URL + an api-key). Callers use this to decide
// whether to dispatch verbs remotely (HTTP) or fall back to the
// in-process registry.
func (c Config) IsConfigured() bool {
	return strings.TrimSpace(c.ServerURL) != "" && strings.TrimSpace(c.Auth.Token) != ""
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
	return cfg, path, nil
}

// ErrNotFound signals that no config file was found at the resolved
// path. Callers fall back to in-process dispatch in this case.
var ErrNotFound = errors.New("cliconfig: not found")

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
