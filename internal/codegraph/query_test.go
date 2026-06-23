package codegraph

import (
	"reflect"
	"testing"
)

// queryGraph builds a small hand-made graph so the query functions can be checked
// against a known structure independent of the disk walk:
//
//	a → b → c
//	a → c
//	d → b
//
// (all paths under module example.com/m). c is a leaf; b's importers are a and d.
func queryGraph() *Graph {
	m := "example.com/m"
	p := func(s string) string { return m + "/" + s }
	return &Graph{
		Module:   m,
		RepoRoot: "/tmp/m",
		Nodes: []Node{
			{ImportPath: p("a"), Dir: "a", Package: "a"},
			{ImportPath: p("b"), Dir: "b", Package: "b"},
			{ImportPath: p("c"), Dir: "c", Package: "c"},
			{ImportPath: p("d"), Dir: "d", Package: "d"},
		},
		Edges: []Edge{
			{From: p("a"), To: p("b")},
			{From: p("a"), To: p("c")},
			{From: p("b"), To: p("c")},
			{From: p("d"), To: p("b")},
		},
	}
}

func p(s string) string { return "example.com/m/" + s }

func TestResolvePackage(t *testing.T) {
	g := queryGraph()
	cases := []struct {
		ref  string
		want string
		ok   bool
	}{
		{"example.com/m/b", p("b"), true},   // exact import path
		{"b", p("b"), true},                 // repo-relative dir
		{"/b/", p("b"), true},               // trimmed slashes
		{"example.com/m/missing", "", false}, // absent
		{"", "", false},                     // empty
	}
	for _, c := range cases {
		got, ok := ResolvePackage(g, c.ref)
		if got != c.want || ok != c.ok {
			t.Errorf("ResolvePackage(%q) = (%q,%v), want (%q,%v)", c.ref, got, ok, c.want, c.ok)
		}
	}
}

func TestResolvePackageAmbiguous(t *testing.T) {
	m := "example.com/m"
	g := &Graph{
		Module: m,
		Nodes: []Node{
			{ImportPath: m + "/x/dup", Dir: "x/dup"},
			{ImportPath: m + "/y/dup", Dir: "y/dup"},
		},
	}
	// "dup" suffix-matches two packages → ambiguous → not resolved.
	if got, ok := ResolvePackage(g, "dup"); ok {
		t.Errorf("ResolvePackage(dup) = (%q,true), want ambiguous (false)", got)
	}
	// The full dir disambiguates.
	if got, ok := ResolvePackage(g, "x/dup"); !ok || got != m+"/x/dup" {
		t.Errorf("ResolvePackage(x/dup) = (%q,%v), want (%q,true)", got, ok, m+"/x/dup")
	}
}

func TestPackageFocus(t *testing.T) {
	g := queryGraph()
	f := Package(g, p("b"))
	if !reflect.DeepEqual(f.DependsOn, []string{p("c")}) {
		t.Errorf("b depends_on = %v, want [c]", f.DependsOn)
	}
	if !reflect.DeepEqual(f.DependedOnBy, []string{p("a"), p("d")}) {
		t.Errorf("b depended_on_by = %v, want [a d]", f.DependedOnBy)
	}
	// A leaf has empty (non-nil) slices for a stable JSON form.
	leaf := Package(g, p("c"))
	if leaf.DependsOn == nil || len(leaf.DependsOn) != 0 {
		t.Errorf("c depends_on = %v, want empty non-nil", leaf.DependsOn)
	}
}

func TestDepsForwardClosure(t *testing.T) {
	g := queryGraph()
	// a pulls in b and c (transitively through b, and directly).
	if got := Deps(g, p("a")); !reflect.DeepEqual(got, []string{p("b"), p("c")}) {
		t.Errorf("Deps(a) = %v, want [b c]", got)
	}
	// c is a leaf — nothing forward.
	if got := Deps(g, p("c")); len(got) != 0 {
		t.Errorf("Deps(c) = %v, want empty", got)
	}
}

func TestRDepsReverseClosure(t *testing.T) {
	g := queryGraph()
	// Who imports c, transitively? a (direct), b (direct), d (→ b → c).
	if got := RDeps(g, p("c")); !reflect.DeepEqual(got, []string{p("a"), p("b"), p("d")}) {
		t.Errorf("RDeps(c) = %v, want [a b d]", got)
	}
	// Nobody imports a.
	if got := RDeps(g, p("a")); len(got) != 0 {
		t.Errorf("RDeps(a) = %v, want empty", got)
	}
}

func TestCyclesNoneOnDAG(t *testing.T) {
	if got := Cycles(queryGraph()); len(got) != 0 {
		t.Errorf("Cycles(DAG) = %v, want none", got)
	}
}

func TestCyclesDetected(t *testing.T) {
	g := queryGraph()
	// Plant a back-edge c → a, making a → b → c → a a cycle.
	g.Edges = append(g.Edges, Edge{From: p("c"), To: p("a")})
	cycles := Cycles(g)
	if len(cycles) != 1 {
		t.Fatalf("Cycles = %v, want exactly 1", cycles)
	}
	// Canonical form rotates to the lexicographically smallest member (a).
	want := []string{p("a"), p("b"), p("c")}
	if !reflect.DeepEqual(cycles[0], want) {
		t.Errorf("cycle = %v, want %v", cycles[0], want)
	}
}

func TestCyclesSelfContainedTwoNode(t *testing.T) {
	m := "example.com/m"
	g := &Graph{
		Module: m,
		Nodes:  []Node{{ImportPath: m + "/x", Dir: "x"}, {ImportPath: m + "/y", Dir: "y"}},
		Edges:  []Edge{{From: m + "/x", To: m + "/y"}, {From: m + "/y", To: m + "/x"}},
	}
	cycles := Cycles(g)
	if len(cycles) != 1 || !reflect.DeepEqual(cycles[0], []string{m + "/x", m + "/y"}) {
		t.Errorf("Cycles(x↔y) = %v, want one [x y]", cycles)
	}
}
