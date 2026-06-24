package codegraph

import (
	"bytes"
	"encoding/json"
	"testing"
)

// TestToJGFConformsToProfile pins the JGF projection shape (epic:codegraph-portable): a
// directed graph, the format marker in graph metadata, nodes as an id-keyed object with a
// label + advisory metadata, and edges as source→target with a relation. This is the
// contract a 3rd-party producer targets and the portal/viewer consume.
func TestToJGFConformsToProfile(t *testing.T) {
	g := &Graph{
		Module: "m", RepoRoot: "/r", Revision: "abc123",
		Nodes: []Node{
			{ImportPath: "m", Package: "main", Files: 2, PublicSymbols: 3, ExternalDeps: 1},
			{ImportPath: "m/internal/cli", Package: "cli", Files: 5, PublicSymbols: 7, ExternalDeps: 4},
		},
		Edges: []Edge{{From: "m/internal/cli", To: "m"}},
	}
	doc := g.ToJGF()
	if !doc.Graph.Directed {
		t.Error("JGF graph must be directed")
	}
	if doc.Graph.Label != "m" {
		t.Errorf("graph label = %q, want m", doc.Graph.Label)
	}
	if doc.Graph.Metadata["format"] != JGFFormatID {
		t.Errorf("graph metadata format = %v, want %s", doc.Graph.Metadata["format"], JGFFormatID)
	}
	// Node keyed by id, carrying a label + metadata.
	cli, ok := doc.Graph.Nodes["m/internal/cli"]
	if !ok {
		t.Fatal("expected node keyed by import path id")
	}
	if cli.Label != "internal/cli" {
		t.Errorf("node label = %q, want internal/cli (short form)", cli.Label)
	}
	if cli.Metadata["package"] != "cli" || cli.Metadata["publicSymbols"] != 7 {
		t.Errorf("node metadata wrong: %+v", cli.Metadata)
	}
	// Edge as source→target with a relation.
	if len(doc.Graph.Edges) != 1 || doc.Graph.Edges[0].Source != "m/internal/cli" ||
		doc.Graph.Edges[0].Target != "m" || doc.Graph.Edges[0].Relation == "" {
		t.Errorf("edge wrong: %+v", doc.Graph.Edges)
	}
}

// TestRenderJGFDeterministic asserts the rendered JGF is byte-stable across runs (the node
// object map marshals with sorted keys; edges are pre-sorted) and is valid JSON.
func TestRenderJGFDeterministic(t *testing.T) {
	g := &Graph{
		Module: "m",
		Nodes:  []Node{{ImportPath: "m/b"}, {ImportPath: "m/a"}, {ImportPath: "m"}},
		Edges:  []Edge{{From: "m/a", To: "m"}, {From: "m/b", To: "m"}},
	}
	var a, b bytes.Buffer
	if err := g.RenderJGF(&a); err != nil {
		t.Fatal(err)
	}
	if err := g.RenderJGF(&b); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Errorf("RenderJGF not byte-stable:\n%s\n---\n%s", a.String(), b.String())
	}
	var sink JGFDocument
	if err := json.Unmarshal(a.Bytes(), &sink); err != nil {
		t.Fatalf("rendered JGF is not valid JSON: %v", err)
	}
}

// TestCSharpToJGF asserts a C# project graph projects to conformant JGF with NO Go-only
// structural fields — language-neutrality is the whole point (epic:codegraph-portable).
func TestCSharpToJGF(t *testing.T) {
	root := fixtureSolution(t)
	g, err := BuildWith(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	doc := g.ToJGF()
	if doc.Graph.Label != "App" {
		t.Errorf("graph label = %q, want App", doc.Graph.Label)
	}
	for _, id := range []string{"Core", "Web", "Tests"} {
		n, ok := doc.Graph.Nodes[id]
		if !ok {
			t.Errorf("missing node %q", id)
			continue
		}
		if _, ok := n.Metadata["files"]; !ok {
			t.Errorf("node %q missing files metadata", id)
		}
	}
	// Re-marshal to ensure the C# JGF round-trips as plain JSON (a 3rd-party/agent consumer).
	if _, err := json.Marshal(doc); err != nil {
		t.Fatalf("C# JGF does not marshal: %v", err)
	}
}
