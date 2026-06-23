package codegraph

import (
	"os"
	"path/filepath"
	"testing"
)

// writeModule lays out a tiny synthetic module on disk for Build to walk.
func writeModule(t *testing.T) string {
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
	write("go.mod", "module example.com/m\n\ngo 1.22\n")
	// Package a imports intra-module b and external fmt; exports Hi + Const.
	write("a/a.go", `package a

import (
	"fmt"

	"example.com/m/b"
)

const Const = 1

func Hi() string { return fmt.Sprint(b.Name) } // exported

func unexported() {} // not counted
`)
	// Package b: one exported var, no imports.
	write("b/b.go", "package b\n\nvar Name = \"b\"\n")
	// A test file must be ignored entirely (no node, no edge, no public count).
	write("b/b_test.go", "package b\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n")
	return root
}

func TestBuildNodesAndEdges(t *testing.T) {
	g, err := Build(writeModule(t))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if g.Module != "example.com/m" {
		t.Fatalf("module = %q, want example.com/m", g.Module)
	}
	nodes := map[string]Node{}
	for _, n := range g.Nodes {
		nodes[n.ImportPath] = n
	}
	if len(nodes) != 2 {
		t.Fatalf("nodes = %d, want 2 (%v)", len(nodes), g.Nodes)
	}

	a, ok := nodes["example.com/m/a"]
	if !ok {
		t.Fatal("missing node a")
	}
	if a.Package != "a" {
		t.Errorf("a.Package = %q, want a", a.Package)
	}
	if a.PublicSymbols != 2 { // Const + Hi; unexported excluded
		t.Errorf("a.PublicSymbols = %d, want 2", a.PublicSymbols)
	}
	if a.ExternalDeps != 1 { // fmt
		t.Errorf("a.ExternalDeps = %d, want 1", a.ExternalDeps)
	}

	b := nodes["example.com/m/b"]
	if b.PublicSymbols != 1 { // Name; the _test.go file is ignored
		t.Errorf("b.PublicSymbols = %d, want 1 (test file must be excluded)", b.PublicSymbols)
	}

	// Exactly one intra-module edge: a → b.
	if len(g.Edges) != 1 || g.Edges[0].From != "example.com/m/a" || g.Edges[0].To != "example.com/m/b" {
		t.Fatalf("edges = %v, want [a→b]", g.Edges)
	}
}

func TestBuildNoGoMod(t *testing.T) {
	if _, err := Build(t.TempDir()); err == nil {
		t.Fatal("Build without go.mod should error")
	}
}
