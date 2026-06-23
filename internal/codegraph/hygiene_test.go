package codegraph

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeHygieneModule lays out a module exercising both test-support rules:
//
//	prod        → lib            (production edge; both kept)
//	tests/helper                 (under tests/ → excluded by default)
//	onlytest                     (production file, imported only from prod_test.go → excluded)
//	prod_test.go imports onlytest (a test-only import)
func writeHygieneModule(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/h\n\ngo 1.22\n")
	write("prod/prod.go", "package prod\n\nimport \"example.com/h/lib\"\n\nvar _ = lib.Name\n")
	write("lib/lib.go", "package lib\n\nvar Name = \"lib\"\n")
	write("tests/helper/helper.go", "package helper\n\nfunc Help() {}\n")
	write("onlytest/onlytest.go", "package onlytest\n\nfunc Fixture() {}\n")
	// A test file that imports onlytest — the ONLY importer of that package.
	write("prod/prod_test.go", "package prod\n\nimport (\n\t\"testing\"\n\n\t\"example.com/h/onlytest\"\n)\n\nfunc TestX(t *testing.T) { onlytest.Fixture() }\n")
	return root
}

func nodeSet(g *Graph) map[string]bool {
	s := map[string]bool{}
	for _, n := range g.Nodes {
		s[n.ImportPath] = true
	}
	return s
}

func TestBuildExcludesTestSupportByDefault(t *testing.T) {
	root := writeHygieneModule(t)
	g, err := Build(root) // default Options{} → exclude test scaffolding
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	nodes := nodeSet(g)
	if !nodes["example.com/h/prod"] || !nodes["example.com/h/lib"] {
		t.Errorf("production packages missing: %v", g.Nodes)
	}
	if nodes["example.com/h/tests/helper"] {
		t.Errorf("tests/helper should be excluded by default")
	}
	if nodes["example.com/h/onlytest"] {
		t.Errorf("onlytest (imported only from _test.go) should be excluded by default")
	}
	// The production edge survives; no edge references a dropped node.
	if len(g.Edges) != 1 || g.Edges[0].From != "example.com/h/prod" || g.Edges[0].To != "example.com/h/lib" {
		t.Errorf("edges = %v, want one prod→lib", g.Edges)
	}
}

func TestBuildIncludeTestsRestoresScaffolding(t *testing.T) {
	root := writeHygieneModule(t)
	g, err := BuildWith(root, Options{IncludeTests: true})
	if err != nil {
		t.Fatalf("BuildWith: %v", err)
	}
	nodes := nodeSet(g)
	for _, want := range []string{
		"example.com/h/prod", "example.com/h/lib",
		"example.com/h/tests/helper", "example.com/h/onlytest",
	} {
		if !nodes[want] {
			t.Errorf("--include-tests should keep %s; nodes = %v", want, g.Nodes)
		}
	}
}

func TestStampPopulatesFreshness(t *testing.T) {
	root := writeHygieneModule(t) // a tmp dir, not a git repo
	g, err := Build(root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if g.GeneratedAt != "" {
		t.Errorf("Build must not stamp GeneratedAt (got %q)", g.GeneratedAt)
	}
	g.Stamp(root)
	if _, err := time.Parse(time.RFC3339, g.GeneratedAt); err != nil {
		t.Errorf("GeneratedAt %q not RFC3339: %v", g.GeneratedAt, err)
	}
	// Revision is best-effort; a non-git tmp dir yields empty, which is allowed.
	if g.Revision != "" {
		t.Logf("revision resolved (unexpected in tmp, but allowed): %q", g.Revision)
	}
}
