// `satellites work init <story>` — engage a story's workflow in this worktree
// (epic:hook-enforcement, story work-init). It is the writer the START-door
// hook diverts to: it ensures the work directory exists and records the
// engagement the gate reads. Deliberately simple — it sets up .satellites/work
// and names the engaged story; it does not (yet) validate the story against the
// substrate or resolve a workflow.
//
// The work directory is satellites.toml-configurable (work_dir) with a default
// of .satellites/work — the toml need not carry the key. Reader (hook) and
// writer (here) resolve it through cliconfig.ResolveWorkDir so they agree.

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/cliconfig"
	"github.com/bobmcallan/satellites/internal/workstate"
	"github.com/spf13/cobra"
)

func init() {
	work := &cobra.Command{
		Use:   "work",
		Short: "Per-worktree engagement state (the .satellites/work the START door reads)",
		Long: `work manages the per-worktree engagement state under .satellites/work —
which story the agent is currently working on. The START-door hook
(satellites hook gate) reads it to decide whether a file edit may proceed.`,
	}

	var (
		configArg string
		status    string
	)
	initCmd := &cobra.Command{
		Use:   "init <story-id>",
		Short: "Engage a story — set up .satellites/work and record the engagement",
		Long: `init engages a story's workflow in this worktree. It ensures the work
directory exists (.satellites/work by default, or satellites.toml work_dir) and
writes engagement.json naming the story, so the START door allows edits.

It is intentionally minimal: it records the engagement; it does not validate the
story against the substrate or resolve its workflow.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, workDir := resolveWorkContext(configArg)
			now := time.Now().UTC().Format(time.RFC3339)
			return runWorkInit(cmd.OutOrStdout(), repoRoot, workDir, args[0], status, now)
		},
	}
	initCmd.Flags().StringVar(&configArg, "config", "", "Path to satellites.toml (resolves repo root + work_dir; defaults to walk-up from CWD).")
	initCmd.Flags().StringVar(&status, "status", "", "Optional workflow status to record in the engagement (advisory).")
	work.AddCommand(initCmd)

	var statusConfigArg string
	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show the open engagements recorded in this repo's event store",
		Long: `status renders the open engagements from the per-repo engagement event
store (.satellites/work/state.db by default, or satellites.toml state_db) — what
each session is working on, its phase, and whether its lease is fresh. It opens
the store read-only and self-initialises an empty one if none exists.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorkStatus(cmd.OutOrStdout(), resolveStateDB(statusConfigArg))
		},
	}
	statusCmd.Flags().StringVar(&statusConfigArg, "config", "", "Path to satellites.toml (resolves repo root + state_db; defaults to walk-up from CWD).")
	work.AddCommand(statusCmd)

	register(work)
}

// resolveStateDB returns the per-repo engagement event-store path, honouring an
// optional satellites.toml state_db (default .satellites/work/state.db). Mirrors
// resolveWorkContext: an unconfigured repo falls back to a CWD-rooted default.
func resolveStateDB(configArg string) string {
	cfg, path, err := cliconfig.Load(configArg)
	if err != nil || strings.TrimSpace(path) == "" {
		return cfg.ResolveStateDB(cwdOrDot())
	}
	return cfg.ResolveStateDB(cliconfig.RepoRootFromConfigPath(path))
}

// runWorkStatus opens the store and renders its open engagements, newest first.
func runWorkStatus(out io.Writer, stateDB string) error {
	s, err := workstate.Open(stateDB)
	if err != nil {
		return fmt.Errorf("work status: open store: %w", err)
	}
	defer s.Close()
	engs, err := s.ListCurrent()
	if err != nil {
		return fmt.Errorf("work status: %w", err)
	}
	if len(engs) == 0 {
		fmt.Fprintln(out, "no open engagements")
		return nil
	}
	now := time.Now().UTC()
	for _, e := range engs {
		lease := "—"
		if !e.LeaseUntil.IsZero() {
			if e.LeaseUntil.After(now) {
				lease = "fresh→" + e.LeaseUntil.Format(time.RFC3339)
			} else {
				lease = "EXPIRED@" + e.LeaseUntil.Format(time.RFC3339)
			}
		}
		fmt.Fprintf(out, "%s  story=%s  phase=%s  lease=%s  session=%s\n",
			e.UpdatedAt.Format(time.RFC3339), e.Story, e.Phase, lease, e.Session)
	}
	return nil
}

// resolveWorkContext returns the repo root and the resolved work directory,
// honouring an optional satellites.toml work_dir (default .satellites/work). An
// unconfigured repo falls back to a CWD-rooted default — the zero Config still
// resolves to the default via ResolveWorkDir.
func resolveWorkContext(configArg string) (repoRoot, workDir string) {
	cfg, path, err := cliconfig.Load(configArg)
	if err != nil || strings.TrimSpace(path) == "" {
		repoRoot = cwdOrDot()
		return repoRoot, cfg.ResolveWorkDir(repoRoot)
	}
	repoRoot = cliconfig.RepoRootFromConfigPath(path)
	return repoRoot, cfg.ResolveWorkDir(repoRoot)
}

func cwdOrDot() string {
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

// runWorkInit ensures the work dir exists and writes the engagement record.
// Testable core: callers pass the resolved workDir + a fixed timestamp.
func runWorkInit(out io.Writer, repoRoot, workDir, storyID, status, now string) error {
	storyID = strings.TrimSpace(storyID)
	if storyID == "" {
		return fmt.Errorf("work init: story id required")
	}
	if _, err := ensureDir(workDir); err != nil {
		return err
	}
	eng := engagement{StoryID: storyID, Status: strings.TrimSpace(status), UpdatedAt: now}
	b, err := json.MarshalIndent(eng, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(workDir, "engagement.json")
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("work init: write %s: %w", path, err)
	}
	shown := path
	if rel, rerr := filepath.Rel(repoRoot, path); rerr == nil {
		shown = rel
	}
	fmt.Fprintf(out, "engaged %s → %s\n", storyID, shown)
	return nil
}
