package codemap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

// SymbolResolver resolves a handler symbol's short name to a repo-relative
// file:line. It is satisfied by the code index (reused, not re-walked — AC3).
type SymbolResolver func(shortName string) (file string, line int, ok bool)

// WalkCLI walks the cobra command tree under root and returns one EntryPoint per
// runnable leaf command. The command path (minus the root name) is the Name. The
// handler file:line comes from the RunE/Run closure's runtime location made
// repo-relative — most commands wrap their named handler in an inline RunE
// closure, so the closure's source file (e.g. cmd_evidence.go) is the right
// data-edge anchor. When the runtime path is not under repoRoot (a binary built
// elsewhere), it falls back to resolving the handler symbol via the index.
// Auto-generated `help`/`completion` are skipped.
func WalkCLI(root *cobra.Command, repoRoot string, resolve SymbolResolver) []EntryPoint {
	var out []EntryPoint
	rootName := root.Name()
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if sub.Hidden || sub.Name() == "help" || sub.Name() == "completion" {
				continue
			}
			if sub.Runnable() {
				name := strings.TrimPrefix(sub.CommandPath(), rootName+" ")
				short := handlerShortName(sub)
				ep := EntryPoint{Surface: SurfaceCLI, Name: name, Handler: short}
				if f, l, ok := handlerLoc(sub, repoRoot); ok {
					ep.File, ep.Line = f, l
				} else if short != "" && resolve != nil {
					if f, l, ok := resolve(short); ok {
						ep.File, ep.Line = f, l
					}
				}
				out = append(out, ep)
			}
			walk(sub)
		}
	}
	walk(root)
	return out
}

// handlerLoc returns the repo-relative file:line of a command's RunE/Run, from
// the runtime program counter. ok is false when the source path is not under
// repoRoot (so the caller falls back to the index).
func handlerLoc(c *cobra.Command, repoRoot string) (string, int, bool) {
	var fn interface{}
	switch {
	case c.RunE != nil:
		fn = c.RunE
	case c.Run != nil:
		fn = c.Run
	default:
		return "", 0, false
	}
	pc := reflect.ValueOf(fn).Pointer()
	f := runtime.FuncForPC(pc)
	if f == nil {
		return "", 0, false
	}
	file, line := f.FileLine(pc)
	if rel, err := filepath.Rel(repoRoot, file); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel), line, true
	}
	return "", 0, false
}

// handlerShortName returns the trailing symbol name of a command's RunE/Run
// (e.g. "runCodeIndex"), or "" if it has no handler. Closures resolve to their
// synthesised name (e.g. "glob..func1") which the index will not match — those
// keep an empty File:Line, which is honest.
func handlerShortName(c *cobra.Command) string {
	var fn interface{}
	switch {
	case c.RunE != nil:
		fn = c.RunE
	case c.Run != nil:
		fn = c.Run
	default:
		return ""
	}
	return FuncShortName(fn)
}

// FuncShortName resolves a function value to its trailing symbol name via
// runtime (e.g. "runCodeIndex", "invokeDocumentGet"). Used for both cobra
// RunE closures and verb Invoke funcs so handler resolution is registry-derived.
func FuncShortName(fn interface{}) string {
	pc := reflect.ValueOf(fn).Pointer()
	f := runtime.FuncForPC(pc)
	if f == nil {
		return ""
	}
	full := f.Name() // e.g. github.com/.../internal/cli.runCodeIndex
	if i := strings.LastIndex(full, "."); i >= 0 {
		return full[i+1:]
	}
	return full
}

// hookSettings mirrors the slice of .claude/settings.json this map reads.
type hookSettings struct {
	Hooks map[string][]struct {
		Matcher string `json:"matcher"`
		Hooks   []struct {
			Type    string `json:"type"`
			Command string `json:"command"`
		} `json:"hooks"`
	} `json:"hooks"`
}

var satellitesCmd = regexp.MustCompile(`satellites\s+((?:[a-z][a-z_-]*)(?:\s+[a-z][a-z_-]*)?)`)

// ParseHooks reads .claude/settings.json and returns (a) one EntryPoint per hook
// command (Surface hook, always live), and (b) the set of `satellites <cmd>`
// invocations the hooks fire — the wiring that makes hook-side CLI commands LIVE
// (the commitgate/hook/gate false-positive the rough grep made). It is a real
// JSON parse of the registry, not a text grep.
func ParseHooks(settingsPath string) (entries []EntryPoint, invoked map[string]Invoker) {
	invoked = map[string]Invoker{}
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		return nil, invoked
	}
	var s hookSettings
	if json.Unmarshal(raw, &s) != nil {
		return nil, invoked
	}
	rel := relOrBase(settingsPath)
	for event, groups := range s.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				for _, m := range satellitesCmd.FindAllStringSubmatch(h.Command, -1) {
					cmd := normaliseCmd(m[1])
					if cmd == "" {
						continue
					}
					entries = append(entries, EntryPoint{
						Surface:  SurfaceHook,
						Name:     event + ": " + cmd,
						Handler:  cmd,
						Invokers: []Invoker{{Source: "settings.json", File: rel}},
					})
					if _, seen := invoked[cmd]; !seen {
						invoked[cmd] = Invoker{Source: "settings.json", File: rel}
					}
				}
			}
		}
	}
	return entries, invoked
}

// muxRoute matches a Go ServeMux registration: mux.HandleFunc("PAT", handler(...))
// or mux.Handle("PAT", ...handler(...)...). The pattern string and the handler
// identifier are captured; this derives routes from the registration source (the
// mux IS the route registry) rather than a hardcoded route list.
var muxRoute = regexp.MustCompile(`mux\.(?:HandleFunc|Handle)\(\s*"([^"]+)"\s*,.*?([A-Za-z][A-Za-z0-9_]*)\(`)

// ParseRoutes reads the server mux registration file and returns one EntryPoint
// per route (Surface server). Routes are externally reachable over HTTP, so each
// carries a synthetic "http" invoker (never an orphan target — the orphan target
// is internal dead features). Handler file:line resolves via the index.
func ParseRoutes(serverGoPath string, resolve SymbolResolver) []EntryPoint {
	raw, err := os.ReadFile(serverGoPath)
	if err != nil {
		return nil
	}
	rel := relOrBase(serverGoPath)
	var out []EntryPoint
	seen := map[string]bool{}
	for _, m := range muxRoute.FindAllStringSubmatch(string(raw), -1) {
		pat, handler := m[1], m[2]
		if seen[pat] {
			continue
		}
		seen[pat] = true
		ep := EntryPoint{
			Surface:  SurfaceServer,
			Name:     pat,
			Handler:  handler,
			Invokers: []Invoker{{Source: "http", File: rel}},
		}
		if resolve != nil {
			if f, l, ok := resolve(handler); ok {
				ep.File, ep.Line = f, l
			}
		}
		out = append(out, ep)
	}
	return out
}

// normaliseCmd trims a captured `satellites <cmd>` to its meaningful command
// path, dropping a trailing connective like "|| exit".
func normaliseCmd(s string) string {
	s = strings.TrimSpace(s)
	fields := strings.Fields(s)
	// Drop a lone shell connective leaking into the 2-token capture.
	if len(fields) == 2 && (fields[1] == "exit" || fields[1] == "true" || fields[1] == "false") {
		fields = fields[:1]
	}
	return strings.Join(fields, " ")
}

func relOrBase(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		if cwd, err := os.Getwd(); err == nil {
			if r, err := filepath.Rel(cwd, abs); err == nil && !strings.HasPrefix(r, "..") {
				return r
			}
		}
	}
	return p
}
