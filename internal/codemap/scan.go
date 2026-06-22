package codemap

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// invokerDirs are the directories whose contents are genuine INVOCATION
// contexts: harness wiring (.claude), the authored process substrate (config —
// skills, the MCP bootstrap/install docs that instruct setup commands,
// workflows), CI, scripts, and Go callers. The repo's docs/ tree and the
// generated command-surface listing (.satellites/documents) are DELIBERATELY
// excluded — a doc that merely LISTS the command surface must never masquerade
// as an invoker (the precision the one-shot report stressed: a false "live" is
// as wrong as a false "orphan"). Verified: no config/.claude file references
// `satellites evidence`, so the dead family stays dead.
var invokerDirs = []string{
	".claude", "config", ".github", "scripts", "examples",
	"internal", "cmd",
}

var invokerFiles = []string{"Makefile", "Dockerfile"}

type corpusFile struct {
	rel     string
	content string
}

func loadCorpus(repoRoot string, dirs []string, files []string) []corpusFile {
	var out []corpusFile
	add := func(path string) {
		b, err := os.ReadFile(path)
		if err != nil {
			return
		}
		out = append(out, corpusFile{rel: relTo(repoRoot, path), content: string(b)})
	}
	for _, d := range dirs {
		root := filepath.Join(repoRoot, d)
		_ = filepath.WalkDir(root, func(path string, de os.DirEntry, err error) error {
			if err != nil || de.IsDir() {
				return nil
			}
			if strings.HasSuffix(path, "_test.go") {
				return nil
			}
			ext := filepath.Ext(path)
			switch ext {
			case ".go", ".md", ".sh", ".yml", ".yaml", ".json", ".toml", ".txt", "":
				add(path)
			}
			return nil
		})
	}
	for _, f := range files {
		add(filepath.Join(repoRoot, f))
	}
	return out
}

func sourceFor(rel string) string {
	switch {
	case strings.HasSuffix(rel, "settings.json"):
		return "settings.json"
	case strings.Contains(rel, "skills"):
		return "skill"
	case strings.HasPrefix(rel, ".github"):
		return "github"
	case strings.HasPrefix(rel, "scripts"):
		return "script"
	case strings.HasPrefix(rel, "examples"):
		return "example"
	case rel == "Makefile" || rel == "Dockerfile":
		return "build"
	case strings.HasSuffix(rel, ".go"):
		return "go"
	default:
		return "doc"
	}
}

// ScanInvokers fills Invokers for every CLI and MCP entry.
//
//   - A CLI command is LIVE when `satellites <name>` appears in a NON-Go
//     invocation context — a hook (settings.json), a process skill, CI, a
//     script, the Makefile. Go files are excluded: a `satellites evidence ci`
//     string in a .go file is help/usage text (a self-reference), never an
//     invocation — counting it was the bug that hid the dead `evidence` family.
//   - An MCP verb is LIVE when its quoted name is dispatched from internal/cmd
//     Go — any `"<verb>"` occurrence that is NOT its registration (`Name: "x"`).
//     This catches cross-verb and server-boot dispatch the cli-only scan missed.
//
// hookInvoked seeds CLI invokers already proven by the settings.json parse.
func ScanInvokers(repoRoot string, entries []EntryPoint, hookInvoked map[string]Invoker) {
	corpus := loadCorpus(repoRoot, invokerDirs, invokerFiles)

	for i := range entries {
		e := &entries[i]
		switch e.Surface {
		case SurfaceCLI:
			if inv, ok := hookInvoked[e.Name]; ok {
				e.Invokers = appendInvoker(e.Invokers, inv)
			}
			re := cliCmdRegexp(e.Name)
			for _, cf := range corpus {
				if strings.HasSuffix(cf.rel, ".go") {
					continue // Go `satellites <cmd>` text is help/usage, not invocation
				}
				if loc := re.FindStringIndex(cf.content); loc != nil {
					e.Invokers = appendInvoker(e.Invokers, Invoker{
						Source: sourceFor(cf.rel), File: cf.rel, Line: lineAt(cf.content, loc[0]),
					})
				}
			}
		case SurfaceMCP:
			reg := `Name:` // the registration form `Name: "x"` is not a caller
			q := `"` + e.Name + `"`
			for _, cf := range corpus {
				if !strings.HasSuffix(cf.rel, ".go") {
					continue
				}
				if loc := dispatchSite(cf.content, q, reg); loc >= 0 {
					e.Invokers = appendInvoker(e.Invokers, Invoker{
						Source: "go", File: cf.rel, Line: lineAt(cf.content, loc),
					})
				}
			}
		}
	}
}

// dispatchSite returns the offset of the first quoted-verb occurrence that is
// NOT on a registration line (one containing reg before the quote), or -1.
func dispatchSite(content, quoted, reg string) int {
	from := 0
	for {
		idx := strings.Index(content[from:], quoted)
		if idx < 0 {
			return -1
		}
		abs := from + idx
		ls := strings.LastIndexByte(content[:abs], '\n') + 1
		le := strings.IndexByte(content[abs:], '\n')
		end := len(content)
		if le >= 0 {
			end = abs + le
		}
		line := content[ls:end]
		if !strings.Contains(line, reg) {
			return abs
		}
		from = abs + len(quoted)
	}
}

// cliCmdRegexp matches `satellites <name>` on a word boundary (so "evidence ci"
// does not match "evidence cite"). Whitespace in the name is flexible.
func cliCmdRegexp(name string) *regexp.Regexp {
	parts := strings.Fields(name)
	for i, p := range parts {
		parts[i] = regexp.QuoteMeta(p)
	}
	return regexp.MustCompile(`satellites\s+` + strings.Join(parts, `\s+`) + `\b`)
}

var (
	reProduce  = regexp.MustCompile(`Kind:\s*"([a-z][a-z0-9_:]*)"`)
	reConsume1 = regexp.MustCompile(`case\s+"([a-z][a-z0-9_:]*)":`)
	reConsume2 = regexp.MustCompile(`Kind\s*==\s*"([a-z][a-z0-9_:]*)"`)
)

// ScanDataEdges attaches Produces/Consumes ledger kinds to each entry by the
// FILE its handler lives in: a `Kind: "x"` literal in the handler file is a
// produced kind; a `case "x":` / `Kind == "x"` is a consumed kind. This is a
// local (per-file) data edge — enough to separate a wholly un-invoked
// producer→consumer cluster (evidence/`ci_result`) from a utility with no data.
//
// It returns the set of every ledger kind that is CONSUMED anywhere in the repo.
// A dead producer→consumer cluster requires a real consumer, so the HIGH-orphan
// verdict only fires for a produced kind that something actually reads — a
// write-only kind with no reader is not a "dead cluster".
func ScanDataEdges(repoRoot string, entries []EntryPoint) map[string]bool {
	produceByFile := map[string]map[string]bool{}
	consumeByFile := map[string]map[string]bool{}
	for _, d := range []string{"internal", "cmd"} {
		root := filepath.Join(repoRoot, d)
		_ = filepath.WalkDir(root, func(path string, de os.DirEntry, err error) error {
			if err != nil || de.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			rel := relTo(repoRoot, path)
			src := string(b)
			for _, m := range reProduce.FindAllStringSubmatch(src, -1) {
				addKind(produceByFile, rel, m[1])
			}
			for _, m := range reConsume1.FindAllStringSubmatch(src, -1) {
				addKind(consumeByFile, rel, m[1])
			}
			for _, m := range reConsume2.FindAllStringSubmatch(src, -1) {
				addKind(consumeByFile, rel, m[1])
			}
			return nil
		})
	}
	consumed := map[string]bool{}
	for _, kinds := range consumeByFile {
		for k := range kinds {
			consumed[k] = true
		}
	}
	for i := range entries {
		e := &entries[i]
		if e.File == "" {
			continue
		}
		e.Produces = sortedKeys(produceByFile[e.File])
		e.Consumes = sortedKeys(consumeByFile[e.File])
	}
	return consumed
}

func addKind(m map[string]map[string]bool, file, kind string) {
	if m[file] == nil {
		m[file] = map[string]bool{}
	}
	m[file][kind] = true
}

func appendInvoker(list []Invoker, inv Invoker) []Invoker {
	for _, x := range list {
		if x.Source == inv.Source && x.File == inv.File {
			return list
		}
	}
	return append(list, inv)
}

func lineAt(s string, off int) int {
	if off > len(s) {
		off = len(s)
	}
	return strings.Count(s[:off], "\n") + 1
}

func relTo(root, path string) string {
	if r, err := filepath.Rel(root, path); err == nil {
		return filepath.ToSlash(r)
	}
	return filepath.ToSlash(path)
}

func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ { // tiny insertion sort, stable + dep-free
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
