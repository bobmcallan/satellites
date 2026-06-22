package codemap

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

// findEntry returns the entry with the given surface+name, or fails.
func findEntry(t *testing.T, m *Map, surface Surface, name string) EntryPoint {
	t.Helper()
	for _, e := range m.Entries {
		if e.Surface == surface && e.Name == name {
			return e
		}
	}
	t.Fatalf("entry %s/%s not found", surface, name)
	return EntryPoint{}
}

// TestClassify_EvidenceShape pins the report's central finding as a regression:
// an un-invoked command that produces a ledger kind read only by other
// un-invoked paths is a HIGH-confidence orphan (the `evidence`/`ci_result`
// shape), while hooks, exposed verbs, write-only and live-consumed data, and
// no-data utilities are classified correctly. Synthetic so it survives the
// later deletion of the real evidence subsystem (sty_d78b0c11).
func TestClassify_EvidenceShape(t *testing.T) {
	m := &Map{
		ConsumedKinds: map[string]bool{"dead_kind": true, "live_kind": true, "shared_kind": true},
		Entries: []EntryPoint{
			// dead cluster: un-invoked producer of a kind only un-invoked paths read
			{Surface: SurfaceCLI, Name: "ghost ci", File: "ghost.go", Produces: []string{"dead_kind"}},
			{Surface: SurfaceCLI, Name: "ghost audit", File: "ghost.go", Produces: []string{"dead_kind"}},
			// a LIVE entry that consumes live_kind (so live_kind is "touched")
			{Surface: SurfaceServer, Name: "GET /live", File: "live.go", Consumes: []string{"live_kind"},
				Invokers: []Invoker{{Source: "http"}}},
			// un-invoked but its produced kind is also read by the live server → medium
			{Surface: SurfaceCLI, Name: "manual gate", File: "gate.go", Produces: []string{"live_kind"}},
			// un-invoked, write-only kind (no consumer in ConsumedKinds-> not) → medium
			{Surface: SurfaceCLI, Name: "writer only", File: "w.go", Produces: []string{"unread_kind"}},
			// un-invoked, no data edges → low
			{Surface: SurfaceCLI, Name: "util", File: "u.go"},
			// hook → never orphan
			{Surface: SurfaceHook, Name: "Stop: hook stopcheck", Invokers: []Invoker{{Source: "settings.json"}}},
			// exposed MCP verb → never orphan
			{Surface: SurfaceMCP, Name: "document_get", Exposed: true},
			// live CLI command (has invoker) → not orphan
			{Surface: SurfaceCLI, Name: "real cmd", Invokers: []Invoker{{Source: "skill"}}},
		},
	}
	m.Classify()

	want := map[string]struct {
		orphan bool
		conf   Confidence
	}{
		"ghost ci":    {true, ConfHigh},
		"ghost audit": {true, ConfHigh},
		"manual gate": {true, ConfMedium},
		"writer only": {true, ConfMedium},
		"util":        {true, ConfLow},
		"real cmd":    {false, ""},
	}
	for _, e := range m.Entries {
		w, ok := want[e.Name]
		if !ok {
			continue
		}
		if e.Orphan != w.orphan || e.Confidence != w.conf {
			t.Errorf("%s: got orphan=%v conf=%q, want orphan=%v conf=%q", e.Name, e.Orphan, e.Confidence, w.orphan, w.conf)
		}
	}
	if got := findEntry(t, m, SurfaceHook, "Stop: hook stopcheck"); got.Orphan {
		t.Errorf("hook must never be an orphan")
	}
	if got := findEntry(t, m, SurfaceMCP, "document_get"); got.Orphan {
		t.Errorf("exposed verb must never be an orphan")
	}
	// Orphans collected and HIGH sorted first.
	if len(m.Orphans) == 0 || m.Orphans[0].Confidence != ConfHigh {
		t.Errorf("expected HIGH orphans first, got %+v", m.Orphans)
	}
}

// TestClassify_DeadFamilyBump: a wholly un-invoked family inherits HIGH from any
// dead-ending member (evidence's reader subcommands die with its writer).
func TestClassify_DeadFamilyBump(t *testing.T) {
	m := &Map{
		ConsumedKinds: map[string]bool{"k": true},
		Entries: []EntryPoint{
			{Surface: SurfaceCLI, Name: "ev ci", File: "ev.go", Produces: []string{"k"}}, // HIGH
			{Surface: SurfaceCLI, Name: "ev show", File: "evshow.go"},                    // would be LOW alone
		},
	}
	m.Classify()
	show := findEntry(t, m, SurfaceCLI, "ev show")
	if show.Confidence != ConfHigh {
		t.Errorf("ev show should bump to HIGH (dead family), got %q", show.Confidence)
	}
}

// TestClassify_FamilyNotBumpedWhenLiveMember: a family with a live member is not
// a dead cluster — its orphan siblings keep their own confidence.
func TestClassify_FamilyNotBumpedWhenLiveMember(t *testing.T) {
	m := &Map{
		ConsumedKinds: map[string]bool{"k": true},
		Entries: []EntryPoint{
			{Surface: SurfaceCLI, Name: "x ci", File: "x.go", Produces: []string{"k"}},    // HIGH
			{Surface: SurfaceCLI, Name: "x list", Invokers: []Invoker{{Source: "skill"}}}, // live
			{Surface: SurfaceCLI, Name: "x show", File: "xshow.go"},                       // low, must stay
		},
	}
	m.Classify()
	if show := findEntry(t, m, SurfaceCLI, "x show"); show.Confidence != ConfLow {
		t.Errorf("x show should stay LOW (family has a live member), got %q", show.Confidence)
	}
}

func TestParseHooks(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	mustWrite(t, settings, `{
	  "hooks": {
	    "PreToolUse": [{"matcher":"Bash","hooks":[{"type":"command","command":"satellites hook commitgate || exit 2"}]}],
	    "Stop":       [{"hooks":[{"type":"command","command":"satellites hook stopcheck"}]}],
	    "SessionStart":[{"hooks":[{"type":"command","command":"satellites code index"}]}]
	  }
	}`)
	entries, invoked := ParseHooks(settings)
	if len(entries) != 3 {
		t.Fatalf("want 3 hook entries, got %d", len(entries))
	}
	if _, ok := invoked["hook commitgate"]; !ok {
		t.Errorf("commitgate should be recorded as settings.json-invoked (the false-positive fix), got %v", keys(invoked))
	}
	if _, ok := invoked["code index"]; !ok {
		t.Errorf("code index should be settings.json-invoked")
	}
	for _, e := range entries {
		if e.Surface != SurfaceHook {
			t.Errorf("hook entry has wrong surface %q", e.Surface)
		}
	}
}

func TestParseRoutes(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "server.go")
	mustWrite(t, f, `package server
func reg(mux *http.ServeMux) {
	mux.HandleFunc("GET /stories/{id}", storyDetailHandler(cfg))
	mux.HandleFunc("/ledger", ledgerHandler(cfg))
	mux.Handle("POST /mcp", wrap(mcpHandler(cfg)))
}`)
	routes := ParseRoutes(f, nil)
	if len(routes) != 3 {
		t.Fatalf("want 3 routes, got %d: %+v", len(routes), routes)
	}
	got := map[string]string{}
	for _, r := range routes {
		got[r.Name] = r.Handler
		if !r.Live() {
			t.Errorf("route %s should be live (http-reachable)", r.Name)
		}
	}
	if got["GET /stories/{id}"] != "storyDetailHandler" {
		t.Errorf("route handler mismatch: %v", got)
	}
}

func TestScanInvokers_CLIIgnoresGoHelpText(t *testing.T) {
	root := t.TempDir()
	// A skill genuinely invokes `satellites foo bar`.
	mustWrite(t, filepath.Join(root, "config", "skills", "s.md"), "run `satellites foo bar` to do it")
	// A Go file mentions it only in help text — must NOT count as an invoker.
	mustWrite(t, filepath.Join(root, "internal", "cli", "cmd_foo.go"),
		"package cli\n// usage: satellites foo bar --flag\nvar x = 1\n")
	// A verb dispatched in Go (not its registration).
	mustWrite(t, filepath.Join(root, "internal", "cli", "caller.go"),
		"package cli\nfunc f(){ dispatchVerb(ctx, \"my_verb\", req) }\n")
	mustWrite(t, filepath.Join(root, "internal", "verb", "my.go"),
		"package verb\nfunc init(){ Register(&Verb{Name: \"my_verb\"}) }\n")

	entries := []EntryPoint{
		{Surface: SurfaceCLI, Name: "foo bar"},
		{Surface: SurfaceMCP, Name: "my_verb"},
		{Surface: SurfaceCLI, Name: "ghost cmd"},
	}
	withWd(t, root, func() {
		ScanInvokers(root, entries, nil)
	})

	foo := entries[0]
	if !foo.Live() {
		t.Fatalf("foo bar should be live via skill")
	}
	for _, inv := range foo.Invokers {
		if inv.Source == "go" {
			t.Errorf("CLI invoker must not come from Go help text, got %+v", foo.Invokers)
		}
	}
	if !entries[1].Live() {
		t.Errorf("my_verb should be live via Go dispatch (not its registration)")
	}
	if entries[2].Live() {
		t.Errorf("ghost cmd should have no invoker")
	}
}

func TestScanDataEdges(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "internal", "cli", "p.go"),
		"package cli\nfunc w(){ append(Entry{Kind: \"made_kind\"}) }\n")
	mustWrite(t, filepath.Join(root, "internal", "trace", "r.go"),
		"package trace\nfunc r(e E){ switch e.Kind { case \"made_kind\": } }\n")
	entries := []EntryPoint{{Surface: SurfaceCLI, Name: "p", File: "internal/cli/p.go"}}
	consumed := ScanDataEdges(root, entries)
	if len(entries[0].Produces) != 1 || entries[0].Produces[0] != "made_kind" {
		t.Errorf("want produces=[made_kind], got %v", entries[0].Produces)
	}
	if !consumed["made_kind"] {
		t.Errorf("made_kind should be in the global consumed set")
	}
}

func TestWalkCLI(t *testing.T) {
	wd, _ := os.Getwd()
	root := &cobra.Command{Use: "satellites"}
	grp := &cobra.Command{Use: "evidence"}
	grp.AddCommand(&cobra.Command{Use: "ci", RunE: func(*cobra.Command, []string) error { return nil }})
	grp.AddCommand(&cobra.Command{Use: "show", RunE: func(*cobra.Command, []string) error { return nil }})
	root.AddCommand(grp)
	root.AddCommand(&cobra.Command{Use: "status", RunE: func(*cobra.Command, []string) error { return nil }})

	entries := WalkCLI(root, wd, nil)
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name] = true
		if e.Surface != SurfaceCLI {
			t.Errorf("wrong surface for %s", e.Name)
		}
	}
	for _, want := range []string{"evidence ci", "evidence show", "status"} {
		if !names[want] {
			t.Errorf("WalkCLI missed %q (got %v)", want, keys2(names))
		}
	}
	if names["evidence"] {
		t.Errorf("non-runnable group 'evidence' must not be an entry point")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func withWd(t *testing.T, dir string, fn func()) {
	t.Helper()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	fn()
}

func keys(m map[string]Invoker) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
func keys2(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
