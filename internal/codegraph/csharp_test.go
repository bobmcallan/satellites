package codegraph

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile writes content to root/rel, creating parent dirs — a tiny test helper for
// laying out a synthetic .NET solution on disk.
func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fixtureSolution lays out a minimal 3-project .NET solution under a temp dir:
//
//	App.sln
//	Core/Core.csproj            (1 PackageReference, no project refs)
//	Web/Web.csproj              (ProjectReference -> Core; ProjectReference -> Missing)
//	Tests/Tests.csproj          (ProjectReference -> Core, -> Web)
//
// plus .cs sources and an obj/ build-output dir that must be skipped.
func fixtureSolution(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "App.sln", "Microsoft Visual Studio Solution File\n")

	writeFile(t, root, "Core/Core.csproj", `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup><AssemblyName>Core</AssemblyName></PropertyGroup>
  <ItemGroup>
    <PackageReference Include="Newtonsoft.Json" Version="13.0.1" />
  </ItemGroup>
</Project>`)
	writeFile(t, root, "Core/Model.cs", "namespace Core { public class Model {} }")

	writeFile(t, root, "Web/Web.csproj", `<Project Sdk="Microsoft.NET.Sdk">
  <ItemGroup>
    <ProjectReference Include="..\Core\Core.csproj" />
    <ProjectReference Include="..\Missing\Missing.csproj" />
  </ItemGroup>
</Project>`)
	writeFile(t, root, "Web/Controller.cs", "namespace Web {}")
	writeFile(t, root, "Web/Startup.cs", "namespace Web {}")
	// An MSBuild build-output dir: its generated .cs must NOT be counted.
	writeFile(t, root, "Web/obj/Web.AssemblyInfo.cs", "// generated")

	writeFile(t, root, "Tests/Tests.csproj", `<Project Sdk="Microsoft.NET.Sdk">
  <ItemGroup>
    <ProjectReference Include="..\Core\Core.csproj" />
    <ProjectReference Include="..\Web\Web.csproj" />
  </ItemGroup>
</Project>`)
	writeFile(t, root, "Tests/Tests.cs", "namespace Tests {}")
	return root
}

func nodeByID(g *Graph, id string) *Node {
	for i := range g.Nodes {
		if g.Nodes[i].ImportPath == id {
			return &g.Nodes[i]
		}
	}
	return nil
}

func hasEdge(g *Graph, from, to string) bool {
	for _, e := range g.Edges {
		if e.From == from && e.To == to {
			return true
		}
	}
	return false
}

// TestBuildCSharpProjectGraph asserts the .csproj project-reference graph: nodes per
// project, edges per <ProjectReference>, the module from the single .sln, file counts that
// skip obj/, the PackageReference external-dep count, and that a reference to a project not
// in the tree (Missing) is dropped rather than dangling.
func TestBuildCSharpProjectGraph(t *testing.T) {
	root := fixtureSolution(t)
	g, err := BuildWith(root, Options{}) // dispatch: no go.mod -> C# path
	if err != nil {
		t.Fatalf("BuildWith (C#): %v", err)
	}
	if g.Module != "App" {
		t.Errorf("module = %q, want App (the single .sln stem)", g.Module)
	}
	wantNodes := []string{"Core", "Tests", "Web"}
	if len(g.Nodes) != len(wantNodes) {
		t.Fatalf("nodes = %d %v, want %v", len(g.Nodes), g.Nodes, wantNodes)
	}
	for i, n := range g.Nodes {
		if n.ImportPath != wantNodes[i] {
			t.Errorf("node[%d] = %q, want %q (sorted)", i, n.ImportPath, wantNodes[i])
		}
	}
	// Edges: Web->Core, Tests->Core, Tests->Web. The Web->Missing reference is dropped.
	for _, e := range [][2]string{{"Web", "Core"}, {"Tests", "Core"}, {"Tests", "Web"}} {
		if !hasEdge(g, e[0], e[1]) {
			t.Errorf("missing edge %s -> %s", e[0], e[1])
		}
	}
	if hasEdge(g, "Web", "Missing") {
		t.Error("edge to out-of-tree project Missing should be dropped")
	}
	if len(g.Edges) != 3 {
		t.Errorf("edges = %d %v, want 3", len(g.Edges), g.Edges)
	}
	if web := nodeByID(g, "Web"); web == nil || web.Files != 2 {
		t.Errorf("Web.Files = %v, want 2 (Controller.cs + Startup.cs, obj/ skipped)", web)
	}
	if core := nodeByID(g, "Core"); core == nil || core.ExternalDeps != 1 {
		t.Errorf("Core.ExternalDeps = %v, want 1 (one PackageReference)", core)
	}
}

// TestBuildCSharpDeterministic asserts a re-run produces a byte-identical graph (nodes +
// edges), matching the Go path's determinism guarantee.
func TestBuildCSharpDeterministic(t *testing.T) {
	root := fixtureSolution(t)
	a, err := buildCSharp(root)
	if err != nil {
		t.Fatal(err)
	}
	b, err := buildCSharp(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Nodes) != len(b.Nodes) || len(a.Edges) != len(b.Edges) {
		t.Fatalf("non-deterministic sizes: %d/%d vs %d/%d", len(a.Nodes), len(a.Edges), len(b.Nodes), len(b.Edges))
	}
	for i := range a.Nodes {
		if a.Nodes[i] != b.Nodes[i] {
			t.Errorf("node[%d] differs: %+v vs %+v", i, a.Nodes[i], b.Nodes[i])
		}
	}
	for i := range a.Edges {
		if a.Edges[i] != b.Edges[i] {
			t.Errorf("edge[%d] differs: %+v vs %+v", i, a.Edges[i], b.Edges[i])
		}
	}
}

// TestBuildWithUnrecognised asserts a tree with neither go.mod nor any .sln/.csproj is a
// clear error, not a Go-only `read go.mod` failure.
func TestBuildWithUnrecognised(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md", "# just docs")
	if _, err := BuildWith(root, Options{}); err == nil {
		t.Fatal("expected an error for a tree with no recognised project")
	}
}
