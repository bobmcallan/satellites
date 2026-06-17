// `satellites rebase` — reset a repo's satellites governance to the clean
// baseline (epic:satellites-backbone 1.6). A developer authors a workflow
// locally and stores it in git; rebase re-establishes the governance surface
// from baseline (or repairs drift). By default it resets the WORKFLOWS
// (archiving the current .satellites/workflows/ — reversibly — and scaffolding
// the gateless baseline) and reconciles the .claude/settings.json HOOKS to the
// set `init` currently defines (the same idempotent merge), which is also how a
// stale settings.json regains a missing hook. Skills are refreshed with
// `satellites skill sync`; rebase prints the reminder rather than running sync
// (server-dependent, already its own command).

package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

func init() {
	var (
		configArg    string
		doWF, doHook bool
	)
	cmd := &cobra.Command{
		Use:   "rebase",
		Short: "Reset this repo's satellites governance to baseline (archive workflows, reconcile settings.json hooks)",
		Long: `rebase re-establishes a repo's satellites governance from the clean
baseline. With no flags it resets BOTH: the workflows (archiving the current
.satellites/workflows/ to .satellites/workflows.archive-N/ and scaffolding the
gateless baseline) and the .claude/settings.json hooks (adding any the current
init defines but the repo is missing — the same idempotent, non-destructive
merge). Scope it with --workflows / --hooks. The archive is reversible — nothing
is deleted. Refresh skills afterwards with ` + "`satellites skill sync`" + `.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !doWF && !doHook { // default: both
				doWF, doHook = true, true
			}
			return runRebase(cmd.OutOrStdout(), initRepoRoot(configArg), doWF, doHook)
		},
	}
	cmd.Flags().StringVar(&configArg, "config", "", "Path to satellites.toml (resolves the repo root; defaults to walk-up from CWD).")
	cmd.Flags().BoolVar(&doWF, "workflows", false, "Reset workflows to the gateless baseline (archiving the current set). Default: on when no scope flag is given.")
	cmd.Flags().BoolVar(&doHook, "hooks", false, "Reconcile .claude/settings.json hooks to the current init set. Default: on when no scope flag is given.")
	register(cmd)
}

// runRebase performs the requested governance reset for repoRoot, reporting each
// action with the same +/= lines init uses. The workflow archive is reversible
// (a rename, not a delete).
func runRebase(out io.Writer, repoRoot string, doWF, doHook bool) error {
	fmt.Fprintf(out, "rebase %s\n", repoRoot)
	if doWF {
		dest, archived, err := archiveWorkflows(repoRoot)
		if err != nil {
			return err
		}
		if archived {
			fmt.Fprintln(out, initLine(true, "archived .satellites/workflows/ → .satellites/"+filepath.Base(dest)))
		}
		added, err := ensureBaselineWorkflow(repoRoot)
		if err != nil {
			return err
		}
		fmt.Fprintln(out, initLine(added, ".satellites/workflows/satellites-baseline-workflow.md (gateless baseline)"))
	}
	if doHook {
		settingsPath := filepath.Join(repoRoot, ".claude", "settings.json")
		for _, h := range hooksToInstall {
			added, err := ensureHookInstalled(settingsPath, h.event, h.matcher, h.command)
			if err != nil {
				return err
			}
			fmt.Fprintln(out, initLine(added, h.label))
		}
	}
	fmt.Fprintln(out, "  → run `satellites skill sync` to refresh skills from the substrate")
	return nil
}

// archiveWorkflows reversibly moves the repo's current .satellites/workflows/ to
// the next free .satellites/workflows.archive-N/ so ensureBaselineWorkflow can
// scaffold a fresh baseline. A no-op (archived=false) when the dir is absent or
// empty. Returns the archive path and whether it moved anything.
func archiveWorkflows(repoRoot string) (string, bool, error) {
	wfDir := filepath.Join(repoRoot, ".satellites", "workflows")
	entries, err := os.ReadDir(wfDir)
	if err != nil || len(entries) == 0 {
		return "", false, nil // nothing to archive
	}
	var dest string
	for n := 1; ; n++ {
		dest = filepath.Join(repoRoot, ".satellites", fmt.Sprintf("workflows.archive-%d", n))
		if _, e := os.Stat(dest); os.IsNotExist(e) {
			break
		}
	}
	if err := os.Rename(wfDir, dest); err != nil {
		return "", false, fmt.Errorf("rebase: archive workflows: %w", err)
	}
	return dest, true, nil
}
