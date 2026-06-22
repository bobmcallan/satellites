// `satellites hook commitgate` — the PreToolUse Bash door (epic:enforcement-
// surface, sty_946fc605 + sty_448d2024). The START door (`satellites hook gate`)
// gates file edits, but it is matched to Edit/Write/NotebookEdit only — file
// mutations done through Bash (`> f`, `>> f`, `tee`, `mv`/`cp`/`rm`/`sed -i`/
// `git mv`) and the `git commit`/`git push` share point run through Bash and slip
// past it. This handler closes both at the honest choke point: it reads the Bash
// `command` and requires the SAME authority the START door requires — a
// lease-fresh engagement in an editable phase for this session — for (1) an
// obvious in-repo file mutation and (2) `git push` (or `git commit` under the
// strict knob), denying otherwise.
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
	"regexp"
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
		Short: "PreToolUse Bash door — block git commit/push AND obvious file-mutating Bash without an editable engagement",
		Long: `commitgate is the PreToolUse handler matched to Bash. It enforces two things,
both requiring a lease-fresh engagement in an editable phase for this session —
the same authority the START door requires for edits:

  1. file mutations — obvious in-repo file-mutating forms the Edit/Write START
     door does not see: output redirection (` + "`>`/`>>`" + `), ` + "`tee`" + `, ` + "`mv`/`cp`/`rm`/`sed -i`/`git mv`" + `.
     Targets resolve through the same boundary/ungated_dirs/cross-repo rules, so
     /tmp, reads, builds and non-governed paths are not gated.
  2. the share point — ` + "`git push`" + ` (or ` + "`git commit`" + ` when the commit_gate toml
     knob is "commit"), the catch-all backstop.

Read-only git and non-mutating non-git Bash pass. Heuristic, not a shell parser.
It fails closed on an unconfigured repo, consistent with the START door.`,
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
	// Strip heredoc bodies + here-string operands before scanning (sty_8f68efc8):
	// their lines are DATA passed to a verb (e.g. a document_upsert body), not
	// shell file operations, so a governed path or redirect-like text inside them
	// must not be read as a mutation. The command BEFORE a `<<` operator survives,
	// so a genuine mutation on the same line is still gated. Both scans below
	// (mutation targets AND the git share point) read the stripped form.
	scan := stripHeredocs(command)

	start := strings.TrimSpace(ev.Cwd)
	if start == "" {
		if wd, err := os.Getwd(); err == nil {
			start = wd
		}
	}

	session := sessionKey(ev.SessionID, ev.ParentSessionID)
	now := time.Now().UTC()
	root, configured := findSatellitesRepoRoot(start)

	// 1. Bash file-mutation gating (sty_448d2024): the START door matches only
	// Edit/Write tools, so obvious in-repo file-mutating Bash forms (redirection,
	// mv/cp/rm/sed -i/tee/git mv) bypass the per-edit gate. Gate each detected
	// target through gateOutcomeEng — which applies the boundary, ungated_dirs,
	// AND cross-repo rules uniformly, so /tmp, reads, builds and non-governed
	// paths are not gated. Only meaningful in a configured repo. Heuristic, not a
	// shell parser; the git share-point gate below is the catch-all backstop.
	if configured {
		for _, t := range bashMutationTargets(scan) {
			if allow, reason, _ := gateOutcomeEng(start, session, t, now); !allow {
				return emitGateDeny(out, "satellites: this Bash command mutates a governed file without an engagement. "+reason)
			}
		}
	}

	// 2. git share-point gating: `git push` (or `git commit` under the strict
	// knob) requires the same authority. Resolve the strict-reach knob from the
	// LOCAL toml — no server round-trip on a per-Bash hook. An unconfigured repo
	// resolves to the default ("push").
	gateCommit := false
	if configured {
		cfg, _, _ := cliconfig.Load(filepath.Join(root, ".satellites", "satellites.toml"))
		gateCommit = cfg.ResolveCommitGate() == cliconfig.CommitGateCommit
	}

	action := classifyGitCommand(scan, gateCommit)
	if action == gitGateNone {
		return nil // read-only git / non-git Bash / ungated commit → allow
	}

	allow, _, eng := gateOutcomeEng(start, session, "", now)
	if !allow {
		return emitGateDeny(out, commitGateReason(action))
	}
	// The session holds a lease-fresh editable engagement — but committing/pushing
	// is authorised only AT the engaged story's commit-push step, not at any
	// editable phase (sty_e925ff09). CommitReady is set where the workflow is
	// resolved (engage/phase-refresh) when the status IS the workflow's
	// `work_skill: commit-push` state. If it is not set, the agent is mid-work:
	// steer it to checkpoint to the ship step first, so one story ↔ one commit ↔
	// one release holds. Fail-closed: a legacy/unresolved engagement (CommitReady
	// false) gates the commit rather than allowing it.
	if !eng.CommitReady {
		return emitGateDeny(out, commitNotReadyReason(action))
	}
	return nil
}

// redirectRe matches an output redirection target: `>` or `>>` (stdout/append),
// NOT preceded by a digit/`&`/`>` (so fd redirects `2>`, dup `>&`, `&>` are
// skipped), followed by the target path (quoted or a bare token). The leading
// class also rules out the second `>` of `>>` starting a fresh match.
var redirectRe = regexp.MustCompile(`(?:^|[^0-9&>])>>?[ \t]*("[^"]*"|'[^']*'|[^ \t;|&<>]+)`)

// heredocOpRe matches a heredoc redirection operator: `<<` or `<<-`, optional
// spaces, then the delimiter — quoted (`<<'EOF'` / `<<"EOF"`) or a bare
// identifier (`<<EOF`). It deliberately does NOT match a here-string `<<<` (the
// trailing `<` fails every delimiter alternative); here-strings are stripped
// separately. Heuristic, in keeping with the rest of this file.
var heredocOpRe = regexp.MustCompile(`<<-?[ \t]*(?:"([^"\n]+)"|'([^'\n]+)'|([A-Za-z_][A-Za-z0-9_]*))`)

// hereStringRe matches a here-string `<<<` and its single operand (quoted or a
// bare token). The operand is DATA fed on stdin, never a shell file operation.
var hereStringRe = regexp.MustCompile(`<<<[ \t]*("[^"]*"|'[^']*'|[^ \t;|&<>]+)`)

// stripHeredocs removes heredoc BODIES, their closing delimiter lines, and
// here-string operands from a Bash command before mutation/git scanning
// (sty_8f68efc8). A heredoc body — the natural way to pass a multi-line document
// to a verb, e.g. `satellites exec document_upsert <<'EOF' … EOF` — is DATA: its
// lines are not shell commands, so a body line that happens to read like a
// redirect (`> path`) or a mutation command (`rm`/`cp`/`mv`/`tee`/`sed -i` of a
// governed path) must not be scanned as one. The command text BEFORE the `<<`
// operator (the real command, e.g. `tee f <<EOF`) is preserved so genuine
// mutations on that line are still gated. Heuristic, not a shell parser: it
// handles the first heredoc per line and degrades to over-stripping (dropping
// data, never inventing a target) on exotic multi-heredoc lines.
func stripHeredocs(command string) string {
	lines := strings.Split(command, "\n")
	var out []string
	for i := 0; i < len(lines); {
		// A here-string is single-line data — drop its operand wherever it appears.
		line := hereStringRe.ReplaceAllString(lines[i], "")

		loc := heredocOpRe.FindStringSubmatchIndex(line)
		if loc == nil {
			out = append(out, line)
			i++
			continue
		}
		// Remove only the `<<DELIM` operator token; keep the rest of the line on
		// BOTH sides, so a real op on the operator line — before it (`tee f <<EOF`)
		// or after it (`cat <<EOF > governed`) — is still scanned. Only the BODY
		// (subsequent lines) is data.
		out = append(out, line[:loc[0]]+line[loc[1]:])
		op := line[loc[0]:loc[1]]
		delim := firstNonEmptySubmatch(line, loc, 1, 2, 3)
		stripTabs := strings.HasPrefix(op, "<<-")
		i++
		// Drop body lines up to and including the closing delimiter. Bash ends the
		// heredoc at a line equal to the delimiter (leading tabs stripped for `<<-`).
		for i < len(lines) {
			body := lines[i]
			cmp := body
			if stripTabs {
				cmp = strings.TrimLeft(body, "\t")
			}
			i++
			if cmp == delim {
				break // closing delimiter consumed
			}
		}
	}
	return strings.Join(out, "\n")
}

// firstNonEmptySubmatch returns the first non-empty capture group among the
// given group indices, using the submatch index slice loc over s.
func firstNonEmptySubmatch(s string, loc []int, groups ...int) string {
	for _, g := range groups {
		if 2*g+1 < len(loc) && loc[2*g] >= 0 {
			if v := s[loc[2*g]:loc[2*g+1]]; v != "" {
				return v
			}
		}
	}
	return ""
}

// bashMutationTargets extracts the obvious in-repo file-mutation targets a Bash
// command writes (sty_448d2024): output redirection (`>`/`>>`), `tee`, `mv`,
// `cp` (dest), `rm`, `sed -i`, `git mv`. Heuristic, not a shell parser — it
// gates the overwhelmingly common direct forms; the commit-gate at the share
// point is the catch-all backstop. Each returned path is resolved + gated by the
// caller through gateOutcomeEng (boundary, ungated_dirs, and cross-repo rules).
func bashMutationTargets(command string) []string {
	var out []string
	for _, seg := range splitShellSegments(command) {
		out = append(out, redirectTargets(seg)...)
		out = append(out, commandMutationTargets(seg)...)
	}
	return out
}

// redirectTargets returns the files a segment writes via `>`/`>>` (excluding
// /dev/* and fd-dup forms). A `>` inside a quoted span is data, not an operator
// (sty_54c65577) — e.g. the literal `'<root>'` in an interpreter `-c` body — so
// such matches are skipped. A real redirect's TARGET may still be quoted
// (`> "out file"`); that quote follows the operator and is captured as before.
func redirectTargets(seg string) []string {
	mask := quoteMask(seg)
	var out []string
	for _, m := range redirectRe.FindAllStringSubmatchIndex(seg, -1) {
		// m[0:2] is the whole match, m[2:4] is group 1 (the target). The operator
		// is the first `>` in the match; skip the match when that `>` is quoted.
		gt := strings.IndexByte(seg[m[0]:m[1]], '>')
		if gt < 0 || mask[m[0]+gt] {
			continue
		}
		p := unquoteArg(seg[m[2]:m[3]])
		if p == "" || strings.HasPrefix(p, "&") || strings.HasPrefix(p, "/dev/") {
			continue
		}
		out = append(out, p)
	}
	return out
}

// commandMutationTargets returns the files a segment's leading command mutates,
// dispatched per command so reads (e.g. cp sources) are not flagged.
func commandMutationTargets(seg string) []string {
	fields := strings.Fields(seg)
	i := 0
	// Skip env-assignment and common wrapper prefixes (FOO=bar sudo nice … cmd).
	for i < len(fields) {
		f := fields[i]
		if !strings.HasPrefix(f, "-") && !strings.HasPrefix(f, "/") && strings.Contains(f, "=") {
			i++
			continue
		}
		switch baseName(f) {
		case "sudo", "env", "command", "nice", "time", "nohup":
			i++
			continue
		}
		break
	}
	if i >= len(fields) {
		return nil
	}
	cmd := baseName(fields[i])
	args := fields[i+1:]
	switch cmd {
	case "mv", "rm":
		return nonFlagArgs(args) // sources removed + dest written / all removed
	case "cp":
		na := nonFlagArgs(args)
		if len(na) > 0 {
			return na[len(na)-1:] // only the destination is written
		}
	case "tee":
		return nonFlagArgs(args) // each file arg is written (skip -a)
	case "sed":
		if hasInPlaceFlag(args) {
			if na := nonFlagArgs(args); len(na) > 0 {
				return na[len(na)-1:] // the in-place file is the last non-flag arg
			}
		}
	case "git":
		if gitSubcommand(seg) == "mv" {
			return gitMvArgs(args)
		}
	}
	return nil
}

// nonFlagArgs returns the non-option args (unquoted), in order.
func nonFlagArgs(args []string) []string {
	var out []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		out = append(out, unquoteArg(a))
	}
	return out
}

// hasInPlaceFlag reports whether a sed arg list carries -i / -i<suffix> / --in-place.
func hasInPlaceFlag(args []string) bool {
	for _, a := range args {
		if a == "-i" || strings.HasPrefix(a, "-i") || strings.HasPrefix(a, "--in-place") {
			return true
		}
	}
	return false
}

// gitMvArgs returns the non-flag args after the `mv` subcommand token.
func gitMvArgs(args []string) []string {
	for i, a := range args {
		if a == "mv" {
			return nonFlagArgs(args[i+1:])
		}
	}
	return nil
}

// baseName strips any directory prefix from a command token (/usr/bin/git → git).
func baseName(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// unquoteArg strips a single layer of matching surrounding quotes.
func unquoteArg(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
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
// for its own leading command. Separators inside a quoted span are ignored
// (sty_54c65577): a `;`/`|` inside an interpreter `-c` body or any quoted
// argument is data, not a separator, so it must not spawn a bogus segment whose
// leading token looks like a mutation command.
func splitShellSegments(s string) []string {
	mask := quoteMask(s)
	var segs []string
	start := 0
	for i := 0; i < len(s); {
		if mask[i] {
			i++
			continue
		}
		if i+1 < len(s) && !mask[i+1] && (s[i:i+2] == "&&" || s[i:i+2] == "||") {
			segs = append(segs, s[start:i])
			i += 2
			start = i
			continue
		}
		if c := s[i]; c == ';' || c == '|' || c == '\n' {
			segs = append(segs, s[start:i])
			i++
			start = i
			continue
		}
		i++
	}
	return append(segs, s[start:])
}

// quoteMask reports, per byte of s, whether that byte lies inside a single- or
// double-quoted span (the surrounding quote bytes are marked inside too). It is
// a heuristic in keeping with the rest of this file — it models neither
// backslash escapes nor `$'...'`; a quote opens a span until the next matching
// quote (or end of string). It exists so shell-operator scanning (`>`, `|`,
// `;`, `&&`) does not fire on an operator that sits inside a quoted argument,
// e.g. the `>` in an interpreter body like `python3 -c "print('<root>')"`.
func quoteMask(s string) []bool {
	mask := make([]bool, len(s))
	var q byte // 0 = outside quotes, else the open-quote char
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case q == 0 && (c == '\'' || c == '"'):
			q = c
			mask[i] = true
		case q != 0 && c == q:
			mask[i] = true
			q = 0
		case q != 0:
			mask[i] = true
		}
	}
	return mask
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

// commitNotReadyReason is the deny message when the session DOES hold a
// lease-fresh editable engagement, but the engaged story is not AT its
// commit-push step. It steers the agent to checkpoint to the ship step (the
// workflow's `work_skill: commit-push` state) before committing/pushing — the
// one-story ↔ one-commit ↔ one-release binding (sty_e925ff09).
func commitNotReadyReason(action gitGateAction) string {
	verb := "push"
	if action == gitGateCommit {
		verb = "commit"
	}
	return fmt.Sprintf("satellites: `git %s` is gated — this session's engaged story is not at its commit-push step. "+
		"Editing is allowed mid-work, but committing/pushing is authorised only AT the workflow's commit-push step "+
		"(the state whose outgoing transition declares `work_skill: commit-push`). Checkpoint the story to that ship step "+
		"(`satellites story status_transition --skill <commit-push gate>`) before %sing, so one story ↔ one commit ↔ one release holds.", verb, verb)
}
