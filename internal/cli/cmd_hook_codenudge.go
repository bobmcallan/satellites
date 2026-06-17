// `satellites hook codenudge` — PreToolUse advisory steering code discovery to
// the symbol index (sty_f52f4b9d / sty_097bf1a3, epic:code-index-replacement).
// Matched to Read|Grep|Bash, it dispatches on the tool:
//
//   - Read (large source file)  → suggest `code symbol`/`search` over reading
//     the whole file.
//   - Grep (symbol-shaped pattern) → suggest `code search`/`code symbol` over a
//     raw text scan for a name.
//   - Bash (`grep`/`rg` with a symbol-shaped pattern) → same — this is the
//     dominant adoption leak, since agents search by shelling out to grep.
//
// Pure chaperone: it NEVER blocks a tool and degrades to a silent no-op whenever
// the repo has no index, the target is not symbol-shaped, or the nudge is turned
// off (`code_nudge_off = true` in satellites.toml). The match for Grep/Bash is
// deliberately TIGHT — a bare identifier only — so ordinary text/regex/config
// searches are left alone. Stat-only; it opens no database, so it stays cheap on
// high-frequency tools.

package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bobmcallan/satellites/internal/cliconfig"
	"github.com/bobmcallan/satellites/internal/codeindex"
	"github.com/spf13/cobra"
)

// codeNudgeMinBytes is the size over which a source-file Read earns a nudge —
// roughly ~600 lines. Below it, reading the file whole is cheap enough that the
// index offers little, so the nudge stays silent to avoid noise.
const codeNudgeMinBytes = 24 * 1024

// symbolShapedRe matches a bare identifier — a single token that looks like a
// code symbol: starts with a letter or underscore, ≥3 chars, no regex
// metacharacters, spaces, path separators, or quotes. The tightness is the
// point: it keeps the Grep/Bash nudge off ordinary text, comment, and config
// searches (which carry spaces, punctuation, or paths).
var symbolShapedRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{2,}$`)

// isSymbolShaped reports whether s is a bare identifier worth steering to the
// symbol index rather than a raw text search.
func isSymbolShaped(s string) bool {
	return symbolShapedRe.MatchString(strings.TrimSpace(s))
}

// newCodeNudgeCmd builds the PreToolUse Read|Grep|Bash advisory handler.
func newCodeNudgeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "codenudge",
		Short: "PreToolUse advisory — steer symbol lookups to `code search`/`code symbol`",
		Long: `codenudge is the PreToolUse handler matched to Read|Grep|Bash. It injects a
non-blocking advisory suggesting satellites code symbol/search when the tool is
about to navigate code by name: a large source-file Read, a symbol-shaped Grep,
or a symbol-shaped grep/rg run via Bash. It NEVER blocks, stays silent unless the
repo has an index, and is disabled by code_nudge_off=true in satellites.toml.`,
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

	var nudge bool
	var msg string
	switch ev.ToolName {
	case "Grep":
		nudge, msg = assessSymbolSearchNudge(ev.Cwd, ev.ToolInput.Pattern)
	case "Bash":
		nudge, msg = assessSymbolSearchNudge(ev.Cwd, bashGrepPattern(ev.ToolInput.Command))
	default: // Read / NotebookEdit / unset → the large-source-file nudge
		nudge, msg = assessCodeNudge(ev.Cwd, ev.ToolInput.path())
	}
	if nudge {
		return emitAdditionalContext(out, "PreToolUse", msg)
	}
	return nil
}

// indexedRepoRoot returns the repo root for cwd ONLY when the nudge should run
// there: the repo is satellites-configured, has a built index, and has not
// turned the nudge off. Empty/false otherwise (fail-open, silent). Shared by
// every nudge path so the off switch and the index precondition are uniform.
func indexedRepoRoot(cwd string) (string, bool) {
	start := strings.TrimSpace(cwd)
	if start == "" {
		if wd, err := os.Getwd(); err == nil {
			start = wd
		}
	}
	root, ok := findSatellitesRepoRoot(start)
	if !ok {
		return "", false // unconfigured repo — advisory does nothing
	}
	cfg, _, _ := cliconfig.Load(filepath.Join(root, ".satellites", "satellites.toml"))
	if cfg.CodeNudgeOff {
		return "", false // operator turned the nudge off
	}
	if _, err := os.Stat(cfg.ResolveIndexDB(root)); err != nil {
		return "", false // no index → `code search` has nothing to offer
	}
	return root, true
}

// assessCodeNudge is the Read decision core (testable): nudge only when the repo
// has an index, the Read target is an indexable source file INSIDE the repo, and
// it is large. Any miss returns no nudge (fail-open, silent).
func assessCodeNudge(cwd, target string) (nudge bool, msg string) {
	target = strings.TrimSpace(target)
	if target == "" {
		return false, ""
	}
	root, ok := indexedRepoRoot(cwd)
	if !ok {
		return false, ""
	}
	start := strings.TrimSpace(cwd)
	if start == "" {
		if wd, err := os.Getwd(); err == nil {
			start = wd
		}
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
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() || info.Size() < codeNudgeMinBytes {
		return false, ""
	}
	return true, "satellites: this is a large indexed source file. For code discovery, " +
		"`satellites code symbol <name>` fetches a declaration's exact source slice and " +
		"`satellites code search <query>` lists matching symbols (name, kind, file:line) — " +
		"usually far cheaper than reading the whole file. Run `satellites code index` first if the index may be stale."
}

// assessSymbolSearchNudge is the Grep/Bash decision core (testable): nudge only
// when the search target is a bare identifier (a symbol) and the repo has an
// index. A non-symbol pattern — regex, a phrase, a path, a string/comment/config
// key — returns no nudge, so ordinary text searches are left alone.
func assessSymbolSearchNudge(cwd, pattern string) (nudge bool, msg string) {
	pattern = strings.TrimSpace(pattern)
	if !isSymbolShaped(pattern) {
		return false, ""
	}
	if _, ok := indexedRepoRoot(cwd); !ok {
		return false, ""
	}
	return true, "satellites: \"" + pattern + "\" looks like a symbol. " +
		"`satellites code search " + pattern + "` lists matching declarations (kind, file:line) and " +
		"`satellites code symbol " + pattern + "` prints the exact source slice — faster than scanning text by name. " +
		"Use Grep/grep for string, comment, or config text."
}

// grepCommands are the search tools whose first non-flag argument is a pattern
// codenudge inspects.
var grepCommands = map[string]bool{"grep": true, "rg": true, "egrep": true, "fgrep": true}

// bashGrepPattern extracts the search pattern from a Bash command when it is a
// grep/rg invocation, or "" otherwise. It honours simple single/double quotes so
// a quoted phrase ("foo bar") reads as one token (and is then rejected as
// non-symbol). A quoted single identifier ("MyType") reads through and nudges.
func bashGrepPattern(command string) string {
	toks := shellTokens(command)
	for i, t := range toks {
		if !grepCommands[t] {
			continue
		}
		for j := i + 1; j < len(toks); j++ {
			if strings.HasPrefix(toks[j], "-") {
				continue // a flag — skip to the pattern argument
			}
			return toks[j] // first non-flag arg is the pattern
		}
		return ""
	}
	return ""
}

// shellTokens splits s into whitespace-separated tokens, honouring single and
// double quotes (no escape handling — enough to read a grep pattern argument).
// Quoted content collapses to a single token, so "func foo" stays one token and
// is rejected as non-symbol downstream.
func shellTokens(s string) []string {
	var toks []string
	var cur strings.Builder
	var quote rune
	inTok := false
	flush := func() {
		if inTok {
			toks = append(toks, cur.String())
			cur.Reset()
			inTok = false
		}
	}
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
			inTok = true
		case r == '\'' || r == '"':
			quote = r
			inTok = true
		case r == ' ' || r == '\t' || r == '\n' || r == '|' || r == ';' || r == '&':
			flush()
		default:
			cur.WriteRune(r)
			inTok = true
		}
	}
	flush()
	return toks
}
