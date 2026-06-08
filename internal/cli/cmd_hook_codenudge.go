// `satellites hook codenudge` — PreToolUse advisory on Read (sty_f52f4b9d,
// epic:code-index). When the agent is about to Read a LARGE source file in a
// repo that HAS a code index, it injects a non-blocking reminder that
// `satellites code symbol`/`search` can fetch the declaration's exact slice
// instead of reading the whole file — the ~10x token win the order:1 spike
// measured for code navigation.
//
// Pure chaperone: it NEVER blocks a Read and degrades to a silent no-op whenever
// the index is absent, the file is small or not source, or the target is outside
// the repo. The hard doors are elsewhere (the START gate); this only nudges
// adoption of the index. Stat-only — it opens no database — so it stays cheap on
// a high-frequency tool.

package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bobmcallan/satellites/internal/cliconfig"
	"github.com/bobmcallan/satellites/internal/codeindex"
	"github.com/spf13/cobra"
)

// codeNudgeMinBytes is the size over which a source-file Read earns a nudge —
// roughly ~600 lines. Below it, reading the file whole is cheap enough that the
// index offers little, so the nudge stays silent to avoid noise.
const codeNudgeMinBytes = 24 * 1024

// newCodeNudgeCmd builds the PreToolUse Read advisory handler.
func newCodeNudgeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "codenudge",
		Short: "PreToolUse Read advisory — suggest `code symbol`/`search` for large indexed source files",
		Long: `codenudge is the PreToolUse handler matched to Read. When the Read target is a
large source file in a repo that already has a .satellites/index.db, it injects
an advisory suggesting satellites code symbol/search instead of reading the whole
file. It NEVER blocks and stays silent unless an index exists and the file is a
large source file inside the repo.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHookCodeNudge(cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
}

func runHookCodeNudge(in io.Reader, out io.Writer) error {
	raw, _ := io.ReadAll(in)
	var ev preToolUseInput
	_ = json.Unmarshal(raw, &ev) // tolerate empty/garbage → silent
	if nudge, msg := assessCodeNudge(ev.Cwd, ev.ToolInput.path()); nudge {
		return emitAdditionalContext(out, "PreToolUse", msg)
	}
	return nil
}

// assessCodeNudge is the pure decision core (testable): nudge only when the repo
// has an index, the Read target is an indexable source file INSIDE the repo, and
// it is large. Any miss returns no nudge (fail-open, silent).
func assessCodeNudge(cwd, target string) (nudge bool, msg string) {
	target = strings.TrimSpace(target)
	if target == "" {
		return false, ""
	}
	start := strings.TrimSpace(cwd)
	if start == "" {
		if wd, err := os.Getwd(); err == nil {
			start = wd
		}
	}
	root, ok := findSatellitesRepoRoot(start)
	if !ok {
		return false, "" // unconfigured repo — advisory does nothing
	}
	abs := absTargetPath(start, target)
	if abs == "" {
		return false, ""
	}
	// Inside the repo tree only — a Read of ~/.claude or anywhere outside is none
	// of this nudge's business.
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false, ""
	}
	if !codeindex.IsIndexable(filepath.ToSlash(rel)) {
		return false, "" // not a source file an extractor handles
	}
	// An index must exist — else `code search` has nothing to offer. Resolve its
	// path through the shared data-dir config so a data_dir override is honoured.
	cfg, _, _ := cliconfig.Load(filepath.Join(root, ".satellites", "satellites.toml"))
	if _, err := os.Stat(cfg.ResolveIndexDB(root)); err != nil {
		return false, ""
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() || info.Size() < codeNudgeMinBytes {
		return false, ""
	}
	return true, "satellites: this is a large indexed source file. For code discovery, " +
		"`satellites code symbol <name>` fetches a declaration's exact source slice and " +
		"`satellites code search <query>` lists matching symbols (name, kind, file:line) — " +
		"usually far cheaper than reading the whole file. Run `satellites code index` first if the index may be stale."
}
