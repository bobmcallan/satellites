// `satellites hook commitgate` — the PreToolUse COMMIT door (epic:enforcement-
// surface, sty_946fc605). The START door (`satellites hook gate`) gates file
// edits, but it is matched to Edit/Write/NotebookEdit only — `git commit` /
// `git push` run through Bash and slip past it, so ungated work can reach `main`
// with the engaged story still at backlog. This handler closes that leak at the
// honest choke point: it reads the Bash `command`, and when a segment runs
// `git push` (or `git commit` under the strict knob) it requires the SAME
// authority the START door requires — a lease-fresh engagement in an editable
// phase for this session — denying otherwise.
//
// It reuses gateOutcomeEng, so the commit door and the edit door share one
// definition of "actively working a started story" (editable, not a hard-coded
// status — workflow-agnostic per process-as-configuration). Read-only git
// (status/log/diff/show/fetch) and all non-git Bash pass untouched. The strict
// reach is a LOCAL toml knob (commit_gate) — never a server round-trip — because
// this fires on every Bash and must stay fast and offline-safe.
//
// Fail closed, consistent with the START door: an unconfigured repo denies a
// gated git command (the install wiring adds `|| exit 2` so a missing/broken
// binary blocks rather than silently allows).

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
	"github.com/spf13/cobra"
)

// gitGateAction is what classifyGitCommand found in a Bash command: nothing to
// gate, a gated push, or a gated commit.
type gitGateAction int

const (
	gitGateNone gitGateAction = iota
	gitGatePush
	gitGateCommit
)

// newCommitGateCmd builds the PreToolUse Bash commit-door handler.
func newCommitGateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "commitgate",
		Short: "PreToolUse Bash COMMIT door — block git commit/push without an editable engagement",
		Long: `commitgate is the PreToolUse handler matched to Bash. It reads the Bash
command and, when it runs ` + "`git push`" + ` (or ` + "`git commit`" + ` when the
commit_gate toml knob is "commit"), requires a lease-fresh engagement in an
editable phase for this session — the same authority the START door requires for
edits. Read-only git and non-git Bash pass. It fails closed on an unconfigured
repo, consistent with the START door.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHookCommitGate(cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
}

// runHookCommitGate reads the PreToolUse Bash event and blocks a gated git
// command when the session has no lease-fresh editable engagement. Always exits
// 0: a block is the structured deny JSON, an allow is no output. The install
// wiring's `|| exit 2` is the only path that exits non-zero (binary missing).
func runHookCommitGate(in io.Reader, out io.Writer) error {
	raw, _ := io.ReadAll(in)
	var ev preToolUseInput
	_ = json.Unmarshal(raw, &ev) // tolerate empty/garbage → allow

	command := strings.TrimSpace(ev.ToolInput.Command)
	if command == "" {
		return nil // not a readable Bash command → nothing to gate
	}

	start := strings.TrimSpace(ev.Cwd)
	if start == "" {
		if wd, err := os.Getwd(); err == nil {
			start = wd
		}
	}

	// Resolve the strict-reach knob from the LOCAL toml — no server round-trip on
	// a per-Bash hook. An unconfigured repo resolves to the default ("push").
	gateCommit := false
	if root, ok := findSatellitesRepoRoot(start); ok {
		cfg, _, _ := cliconfig.Load(filepath.Join(root, ".satellites", "satellites.toml"))
		gateCommit = cfg.ResolveCommitGate() == cliconfig.CommitGateCommit
	}

	action := classifyGitCommand(command, gateCommit)
	if action == gitGateNone {
		return nil // read-only git / non-git Bash / ungated commit → allow
	}

	session := sessionKey(ev.SessionID, ev.ParentSessionID)
	allow, _, _ := gateOutcomeEng(start, session, "", time.Now().UTC())
	if allow {
		return nil
	}
	return emitGateDeny(out, commitGateReason(action))
}

// classifyGitCommand reports whether a Bash command runs a gated git command.
// It splits the command into shell segments and inspects each segment's git
// subcommand: `push` is always gated; `commit` is gated only when gateCommit is
// set. push takes precedence (it is the share point) so a compound
// `git commit && git push` reports push even when commits are not gated.
//
// Heuristic, not a shell parser: it cannot see through `bash -c "..."` or a git
// invocation hidden in a quoted string. The START door and the Stop-hook
// backstop (sty_b713f886) cover what slips past; this gates the overwhelmingly
// common direct forms (including the commit-push skill's `git commit && git push`).
func classifyGitCommand(command string, gateCommit bool) gitGateAction {
	sawCommit := false
	for _, seg := range splitShellSegments(command) {
		switch gitSubcommand(seg) {
		case "push":
			return gitGatePush
		case "commit":
			sawCommit = true
		}
	}
	if gateCommit && sawCommit {
		return gitGateCommit
	}
	return gitGateNone
}

// splitShellSegments breaks a command line on the shell separators that start a
// new simple command (&&, ||, ;, |, newline), so each segment can be inspected
// for its own leading command. `||` is listed before `|` so the two-char form
// wins at a position.
func splitShellSegments(s string) []string {
	repl := strings.NewReplacer("&&", "\n", "||", "\n", ";", "\n", "|", "\n")
	return strings.Split(repl.Replace(s), "\n")
}

// gitSubcommand returns the git subcommand a single segment runs (lowercased),
// or "" when the segment is not a git invocation. It finds the `git` token
// (allowing an absolute path like /usr/bin/git), then the first non-option token
// after it — skipping git global options that take a value (-C, -c, --git-dir,
// --work-tree, --namespace).
func gitSubcommand(seg string) string {
	fields := strings.Fields(seg)
	for i := 0; i < len(fields); i++ {
		base := fields[i]
		if slash := strings.LastIndex(base, "/"); slash >= 0 {
			base = base[slash+1:] // /usr/bin/git → git
		}
		if base != "git" {
			continue
		}
		for j := i + 1; j < len(fields); j++ {
			a := fields[j]
			switch a {
			case "-C", "-c", "--git-dir", "--work-tree", "--namespace":
				j++ // skip this option's value (loop post-increment skips one more)
				continue
			}
			if strings.HasPrefix(a, "-") {
				continue // a value-less global option
			}
			return strings.ToLower(a)
		}
		return ""
	}
	return ""
}

// commitGateReason is the deny message — it names the blocked verb and the way
// out (engage + advance a story to an editable phase).
func commitGateReason(action gitGateAction) string {
	verb := "push"
	if action == gitGateCommit {
		verb = "commit"
	}
	return fmt.Sprintf("satellites: `git %s` is gated — this session has no lease-fresh engagement in an editable phase. "+
		"Work shared by %s belongs to a gated story, not an ungated %s. Engage a story and advance it to an editable phase "+
		"(`satellites work init <story>`, then its plan/start gates) before %sing.", verb, verb, verb, verb)
}
