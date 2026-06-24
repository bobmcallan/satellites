// C# / .NET support for the language-agnostic codegraph (epic:codegraph-portable,
// story 4). Where the Go path reads import edges from go/ast, the .NET path reads the
// REAL module graph from MSBuild project files: one node per `.csproj` (an
// assembly/project) and one edge per `<ProjectReference>`. This is the deterministic,
// zero-token, in-binary extraction spike 1 selected — the same canonical Graph schema the
// Go path emits, so the viewer + query layer are language-neutral.
//
// Scope: the project-reference graph only. Finer namespace/`using` edges (via the
// tree-sitter runtime the index uses) are a deliberate future enrichment, not this proof.
package codegraph

import (
	"encoding/xml"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// csharpSkipDirs are directories never walked for a .NET tree — the shared VCS/working
// set plus the MSBuild build outputs (obj/, bin/) whose generated `.cs` and copied
// `.csproj` are noise for the source structure.
var csharpSkipDirs = map[string]bool{
	".git":         true,
	".satellites":  true,
	"vendor":       true,
	"node_modules": true,
	"obj":          true,
	"bin":          true,
}

// csprojXML is the minimal MSBuild project shape codegraph reads: the ProjectReference
// edges and the PackageReference (external/NuGet) dependencies. Other MSBuild content is
// ignored — encoding/xml drops unmapped elements.
type csprojXML struct {
	ItemGroups []struct {
		ProjectRefs []struct {
			Include string `xml:"Include,attr"`
		} `xml:"ProjectReference"`
		PackageRefs []struct {
			Include string `xml:"Include,attr"`
		} `xml:"PackageReference"`
	} `xml:"ItemGroup"`
}

// hasCSharpProjects reports whether the tree rooted at root carries a .NET solution or
// project (a `.sln` or any `.csproj`), so BuildWith can dispatch to the C# builder.
func hasCSharpProjects(root string) bool {
	found := false
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if csharpSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if ext := strings.ToLower(filepath.Ext(d.Name())); ext == ".csproj" || ext == ".sln" {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// buildCSharp builds the project-reference graph for the .NET tree rooted at root: one
// node per `.csproj`, one edge per `<ProjectReference>`. The module name is the single
// `.sln` stem when exactly one solution is present, else the repo dir name. Nodes and
// edges are sorted so the output is byte-stable across runs (matching the Go path).
func buildCSharp(root string) (*Graph, error) {
	var csprojPaths, slnStems []string
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if csharpSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		switch strings.ToLower(filepath.Ext(d.Name())) {
		case ".csproj":
			csprojPaths = append(csprojPaths, path)
		case ".sln":
			slnStems = append(slnStems, projectStem(d.Name()))
		}
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("codegraph: walk: %w", walkErr)
	}
	if len(csprojPaths) == 0 {
		return nil, fmt.Errorf("codegraph: no .csproj under %s", root)
	}

	module := filepath.Base(root)
	if len(slnStems) == 1 {
		module = slnStems[0]
	}

	known := map[string]bool{} // project stems that are real nodes (have a .csproj)
	for _, p := range csprojPaths {
		known[projectStem(filepath.Base(p))] = true
	}

	g := &Graph{Module: module, RepoRoot: root}
	seen := map[string]bool{}
	for _, p := range csprojPaths {
		id := projectStem(filepath.Base(p))
		dir := filepath.ToSlash(relDir(root, p))
		refs, pkgs := parseCsproj(p)

		g.Nodes = append(g.Nodes, Node{
			ImportPath:    id,
			Dir:           dir,
			Package:       id,
			Files:         countCSharpFiles(filepath.Dir(p)),
			PublicSymbols: 0, // a Go-specific notion; C# symbol enrichment is out of scope
			ExternalDeps:  len(pkgs),
		})
		for _, to := range refs {
			if to == id || !known[to] {
				continue // self or a reference to a project not in this tree
			}
			key := id + "\x00" + to
			if seen[key] {
				continue
			}
			seen[key] = true
			g.Edges = append(g.Edges, Edge{From: id, To: to})
		}
	}

	sort.Slice(g.Nodes, func(i, j int) bool { return g.Nodes[i].ImportPath < g.Nodes[j].ImportPath })
	sort.Slice(g.Edges, func(i, j int) bool {
		if g.Edges[i].From != g.Edges[j].From {
			return g.Edges[i].From < g.Edges[j].From
		}
		return g.Edges[i].To < g.Edges[j].To
	})
	return g, nil
}

// parseCsproj reads one `.csproj`, returning the project stems it references (edge
// targets, deduped) and its distinct external/NuGet package ids (the ExternalDeps count).
// An unreadable or unparseable file yields no refs — non-fatal, mirroring the Go walk.
func parseCsproj(path string) (refs, pkgs []string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil
	}
	var proj csprojXML
	if err := xml.Unmarshal(data, &proj); err != nil {
		return nil, nil
	}
	refSet, pkgSet := map[string]bool{}, map[string]bool{}
	for _, ig := range proj.ItemGroups {
		for _, r := range ig.ProjectRefs {
			if stem := projectStem(includeBase(r.Include)); stem != "" {
				refSet[stem] = true
			}
		}
		for _, p := range ig.PackageRefs {
			if id := strings.TrimSpace(p.Include); id != "" {
				pkgSet[id] = true
			}
		}
	}
	for s := range refSet {
		refs = append(refs, s)
	}
	for p := range pkgSet {
		pkgs = append(pkgs, p)
	}
	return refs, pkgs
}

// countCSharpFiles counts the `.cs` source files directly under a project tree, skipping
// the MSBuild build-output dirs (obj/, bin/) so generated sources are not counted.
func countCSharpFiles(projDir string) int {
	n := 0
	_ = filepath.WalkDir(projDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if csharpSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(filepath.Ext(d.Name()), ".cs") {
			n++
		}
		return nil
	})
	return n
}

// includeBase normalises an MSBuild Include path (Windows-style backslashes, e.g.
// `..\Meridian.Domain\Meridian.Domain.csproj`) to its final path element.
func includeBase(include string) string {
	include = strings.ReplaceAll(strings.TrimSpace(include), `\`, "/")
	return filepath.Base(include)
}

// projectStem strips a `.csproj`/`.sln` extension from a file name, yielding the project
// (assembly) name used as the node id and edge endpoint.
func projectStem(name string) string {
	return strings.TrimSuffix(strings.TrimSuffix(name, ".csproj"), ".sln")
}
