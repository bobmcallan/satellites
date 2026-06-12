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

	// ReviewerModel, when set, is passed as `--model <value>` to every
	// reviewer `claude -p` run — the gate dispatcher and the step summariser
	// (sty_c7a5d741). NOT required in the toml — empty means no flag, so the
	// reviewer lane inherits the harness default (e.g. the model
	// .claude/settings.json pins).
	ReviewerModel string `toml:"reviewer_model"`

	// SkillsRoot overrides where `skill sync` / `deploy` materialise
	// substrate skills. Empty means the default — <repo>/.claude/skills,
	// anchored to the directory holding .satellites/, NOT the process CWD.
	// A relative value is resolved against that repo root; absolute is used
	// verbatim.
	SkillsRoot string `toml:"skills_root"`

	// LibraryPins names the shared-library skills this repo consumes as-is,
	// each "<publisher>/<name>" (publisher = the publishing project id).
	// `skill sync` materialises pinned skills at lowest precedence — a
	// same-named project/local skill wins — and removing a pin removes the
	// materialised copy on the next sync (epic:skill-library, sty_56855694).
	LibraryPins []string `toml:"library_pins"`

	// WorkDir overrides where per-worktree engagement state lives (the
	// `.satellites/work` the START-door hook reads and `satellites work init`
	// writes). NOT required in the toml — empty means the default
	// <repo>/.satellites/work. A relative value resolves against the repo root;
	// absolute is used verbatim. Reader (hook) and writer (work init) both
	// resolve through ResolveWorkDir so they always agree.
	WorkDir string `toml:"work_dir"`

	// DataDir is the single home for the client's per-repo data stores —
	// state.db (engagement cache) and index.db (code symbol index). NOT required
	// in the toml — empty means the default <repo>/.satellites. A relative value
	// resolves against the repo root; absolute is used verbatim. Both stores
	// resolve under it (ResolveStateDB / ResolveIndexDB), so a custom data_dir
	// relocates them together. Per-story working state stays under
	// .satellites/work (WorkDir), independent of this.
	DataDir string `toml:"data_dir"`

	// StateDB is a per-store OVERRIDE for the engagement event store (SQLite).
	// NOT required in the toml — empty means <data_dir>/state.db (default
	// <repo>/.satellites/state.db, zero-config). A relative value resolves against
	// the repo root; absolute is used verbatim. The store self-initializes on
	// open. Prefer data_dir; this override exists for the rare split layout.
	StateDB string `toml:"state_db"`

	// UngatedDirs lists paths the START-door (`satellites hook gate`) will NOT
	// gate, beyond the default boundary rule. The door already only governs edits
	// INSIDE this repo's tree — anything outside the repo root (e.g. ~/.claude,
	// Claude's own config + agent memory) is ungated by default, so Claude can
	// self-maintain. This list ungates EXTRA paths (including in-repo ones):
	// filepath globs, with a leading ~ expanded to $HOME. Optional; empty means
	// only the default boundary applies (sty_11a6077c).
	UngatedDirs []string `toml:"ungated_dirs"`

	// CommitGate sets how far the commit-gate (`satellites hook commitgate`,
	// epic:enforcement-surface) reaches. "" / "push" (default) gates only
	// `git push` — the share point; "commit" additionally gates `git commit`,
	// so even local commits require a lease-fresh editable engagement. Read from
	// the LOCAL toml (not the server) because the PreToolUse hook fires on every
	// Bash and must be fast + offline-safe. See ResolveCommitGate.
	CommitGate string `toml:"commit_gate"`

	// Measure configures measure mode — the client's session observability.
	// It is DEFAULT ON: an absent [measure] section means enabled + record.
	Measure MeasureConfig `toml:"measure"`

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

// DefaultLogDir is the in-repo session-log location when log_path is unset.
// Session data belongs in the consumer repo (alongside .satellites/work/),
// NOT in $HOME — see ResolveLogDir.
const DefaultLogDir = ".satellites/logs"

// ResolveLogDir is the ONE authoritative computation of where the client
// writes session/measure logs. It is the log_path counterpart of how
// skills_root is anchored: a relative log_path (or the default) resolves
// against the repo root — the directory that HOLDS .satellites/ — never the
// process CWD; an absolute log_path is used verbatim.
//
// This exists because log_path was previously parsed but never consumed, so
// session data had no in-repo home and fell to the separate satellites-client
// daemon's $HOME path (~/.satellites/daemon/logs) — out of this binary's
// control. Routing every consumer through here keeps session data in the repo.
func (c Config) ResolveLogDir(repoRoot string) string {
	p := strings.TrimSpace(c.LogPath)
	if p == "" {
		p = DefaultLogDir
	}
	if filepath.IsAbs(p) {
		return p
	}
	if strings.TrimSpace(repoRoot) == "" {
		repoRoot = "."
	}
	return filepath.Join(repoRoot, p)
}

// DefaultWorkDir is the per-worktree engagement-state location when work_dir
// is unset — repo-relative, alongside the other .satellites/ working state.
const DefaultWorkDir = ".satellites/work"

// ResolveWorkDir is the ONE authoritative computation of where engagement
// state lives, shared by the START-door hook (reader) and `satellites work
// init` (writer) so they never disagree. An explicit work_dir wins
// (repo-relative or absolute); otherwise it defaults to <repo>/.satellites/work.
// Mirrors ResolveLogDir: a relative value resolves against the repo root — the
// directory that HOLDS .satellites/ — never the process CWD.
func (c Config) ResolveWorkDir(repoRoot string) string {
	p := strings.TrimSpace(c.WorkDir)
	if p == "" {
		p = DefaultWorkDir
	}
	if filepath.IsAbs(p) {
		return p
	}
	if strings.TrimSpace(repoRoot) == "" {
		repoRoot = "."
	}
	return filepath.Join(repoRoot, p)
}

// CommitGate knob values. CommitGatePush (the default) gates only `git push`;
// CommitGateCommit additionally gates `git commit`.
const (
	CommitGatePush   = "push"
	CommitGateCommit = "commit"
)

// ResolveCommitGate returns the normalised commit-gate reach: "commit" when the
// toml sets commit_gate to that (gate commits too), else "push" (the default —
// gate only the share point). Any unrecognised value falls back to the safe
// default rather than erroring, so a typo never disables the push gate.
func (c Config) ResolveCommitGate() string {
	if strings.EqualFold(strings.TrimSpace(c.CommitGate), CommitGateCommit) {
		return CommitGateCommit
	}
	return CommitGatePush
}

// DefaultDataDir is where the client's per-repo data stores (state.db, index.db)
// live when data_dir is unset — the repo's .satellites/ directory, beside the
// committed substrate (documents/skills/principles). Both global caches share
// this home; per-story working state stays under .satellites/work (DefaultWorkDir).
const DefaultDataDir = ".satellites"

// ResolveDataDir is the ONE authoritative computation of the directory holding
// the client's per-repo data stores. An explicit data_dir wins (repo-relative or
// absolute); otherwise it defaults to <repo>/.satellites. Mirrors
// ResolveWorkDir/ResolveLogDir: a relative value resolves against the repo root —
// the directory that HOLDS .satellites/ — never the process CWD. The zero-value
// Config resolves to the default (zero-config).
func (c Config) ResolveDataDir(repoRoot string) string {
	p := strings.TrimSpace(c.DataDir)
	if p == "" {
		p = DefaultDataDir
	}
	if filepath.IsAbs(p) {
		return p
	}
	if strings.TrimSpace(repoRoot) == "" {
		repoRoot = "."
	}
	return filepath.Join(repoRoot, p)
}

// DefaultStateDB is the engagement event-store location when neither state_db nor
// data_dir is set — <repo>/.satellites/state.db, beside index.db. Used as the
// fallback by readers that cannot load the config (e.g. the START-door hook).
const DefaultStateDB = ".satellites/state.db"

// ResolveStateDB is the ONE authoritative computation of where the engagement
// event store (SQLite) lives, shared by every reader/writer so they never
// disagree. An explicit state_db wins (repo-relative or absolute) as a per-store
// override; otherwise the store defaults to <data_dir>/state.db — i.e.
// <repo>/.satellites/state.db, beside index.db. A relative value resolves against
// the repo root — the directory that HOLDS .satellites/ — never the process CWD.
// The zero-value Config resolves to the default, so an unconfigured repo still
// lands a working path (zero-config).
func (c Config) ResolveStateDB(repoRoot string) string {
	if p := strings.TrimSpace(c.StateDB); p != "" {
		if filepath.IsAbs(p) {
			return p
		}
		if strings.TrimSpace(repoRoot) == "" {
			repoRoot = "."
		}
		return filepath.Join(repoRoot, p)
	}
	return filepath.Join(c.ResolveDataDir(repoRoot), "state.db")
}

// ResolveIndexDB is the authoritative computation of where the code symbol index
// (SQLite) lives: <data_dir>/index.db — <repo>/.satellites/index.db by default,
// beside state.db. Resolving through data_dir means a custom data_dir relocates
// both stores together.
func (c Config) ResolveIndexDB(repoRoot string) string {
	return filepath.Join(c.ResolveDataDir(repoRoot), "index.db")
}

// RepoRootFromConfigPath derives the repo root (the directory holding
// .satellites/) from a resolved satellites.toml path of the shape
// <repo>/.satellites/satellites.toml. Empty path → "." (CWD), matching the
// unconfigured-repo fallback used elsewhere.
func RepoRootFromConfigPath(configPath string) string {
	if strings.TrimSpace(configPath) == "" {
		return "."
	}
	return filepath.Dir(filepath.Dir(configPath))
}

// MeasureConfig is the [measure] table — measure mode's client config.
// Measure mode is DEFAULT ON: an absent section (zero value: Enabled nil,
// Mode "") resolves to enabled + record. You opt out with enabled=false or
// mode="off".
type MeasureConfig struct {
	// Enabled is a pointer so "absent" (nil) is distinguishable from an
	// explicit false. nil ⇒ default ON.
	Enabled *bool `toml:"enabled"`
	// Mode is off|record|enforce; empty defaults to record.
	Mode string `toml:"mode"`
	// LogDir overrides where session logs are written; empty falls back to
	// log_path / DefaultLogDir via Config.MeasureLogDir.
	LogDir string `toml:"log_dir"`
	// ReviewerSkill names a custom session-review skill (optional).
	ReviewerSkill string `toml:"reviewer_skill"`
}

// ResolveMode returns the effective mode, defaulting empty to "record".
func (m MeasureConfig) ResolveMode() string {
	if s := strings.ToLower(strings.TrimSpace(m.Mode)); s != "" {
		return s
	}
	return "record"
}

// IsEnabled reports whether measure mode should collect data. Default ON:
// enabled unless explicitly disabled (enabled=false) or mode="off".
func (m MeasureConfig) IsEnabled() bool {
	if m.Enabled != nil && !*m.Enabled {
		return false
	}
	return m.ResolveMode() != "off"
}

// validMeasureMode reports whether a (non-empty) mode string is one of the
// three accepted values. Empty is valid (defaults to record).
func validMeasureMode(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "off", "record", "enforce":
		return true
	}
	return false
}

// MeasureLogDir resolves the directory measure-mode session logs are written
// to: an explicit measure.log_dir (repo-relative or absolute) wins, otherwise
// it falls back to ResolveLogDir (log_path / DefaultLogDir). One owner for the
// measure log location.
func (c Config) MeasureLogDir(repoRoot string) string {
	d := strings.TrimSpace(c.Measure.LogDir)
	if d == "" {
		return c.ResolveLogDir(repoRoot)
	}
	if filepath.IsAbs(d) {
		return d
	}
	if strings.TrimSpace(repoRoot) == "" {
		repoRoot = "."
	}
	return filepath.Join(repoRoot, d)
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
	if !validMeasureMode(cfg.Measure.Mode) {
		return Config{}, path, fmt.Errorf("cliconfig: %s: invalid measure.mode %q (want off, record, or enforce)", path, cfg.Measure.Mode)
	}
	// Resolve the bearer from the user-level credential store keyed by
	// server_url (provisioned by `satellites auth`). A missing credential
	// is not an error — Token stays empty and IsConfigured reports false,
	// so the caller falls back to in-process dispatch.
	if cred, err := LoadCredential(cfg.ServerURL); err == nil {
		cfg.Token = cred.Token
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
