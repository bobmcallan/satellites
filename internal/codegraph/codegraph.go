// Package codegraph builds a repo-agnostic, HIGH-LEVEL code graph: one node per
// Go package (its public surface) and one edge per intra-module import (package A
// imports package B). It is the data the codegraph document skill (C2,
// epic:phases-task-outputs) renders as a markdown + mermaid dependency diagram —
// "not function contents", the package-level map of how the codebase fits together.
//
// Like internal/codemap it is an ON-DEMAND build (a `go/ast` walk emitting JSON),
// not a stored index: `satellites code graph` rebuilds it each run, so there is no
// schema migration and no incremental edge cache. The binary emits FACTS (A imports
// B); rendering and opinion live in the substrate (the codegraph skill), never here.
//
// Go-first: the walk parses `.go` sources via go/ast (the same language the native
// indexer covers). Test files (`_test.go`) are excluded so the graph is the
// production dependency structure, not test wiring.
package codegraph

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// skipDirs are directories never walked — VCS metadata, the satellites working
// tree, and vendored / dependency trees whose packages are noise for THIS repo's
// structure. Mirrors internal/codeindex's skip set.
var skipDirs = map[string]bool{
	".git":         true,
	".satellites":  true,
	"vendor":       true,
	"node_modules": true,
}

// Node is one Go package in the graph: where it lives and how large its public
// surface is. ImportPath is the module-qualified path (the edge endpoint id).
type Node struct {
	ImportPath    string `json:"import_path"`
	Dir           string `json:"dir"`     // repo-relative, forward-slashed
	Package       string `json:"package"` // package name (clause)
	Files         int    `json:"files"`
	PublicSymbols int    `json:"public_symbols"` // exported top-level declarations
	ExternalDeps  int    `json:"external_deps"`  // distinct non-module imports (stdlib + third-party)
}

// Edge is one intra-module import: package From imports package To (both
// ImportPaths). External / stdlib imports are not edges (they are counted on the
// node as ExternalDeps), keeping the diagram to this repo's own structure.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Graph is the full package-level result for one module. GeneratedAt and Revision
// are provenance stamped at render time (Stamp); they are omitempty so a pure Build
// result and the query subsets stay byte-stable.
type Graph struct {
	Module      string `json:"module"`
	RepoRoot    string `json:"repo_root"`
	GeneratedAt string `json:"generated_at,omitempty"` // RFC3339 UTC, set by Stamp
	Revision    string `json:"revision,omitempty"`     // short git rev, best-effort
	Nodes       []Node `json:"nodes"`
	Edges       []Edge `json:"edges"`
}

// Options tunes a Build. IncludeTests keeps test-support packages (those under a
// tests/ dir, or imported only from _test.go files and never from production code)
// that Build drops by default, so the graph is the production dependency structure.
type Options struct {
	IncludeTests bool
}

// Stamp records provenance on the graph: the generation time (RFC3339 UTC) and the
// repo's short git revision (best-effort — left empty when repoRoot is not a git repo
// or git is unavailable). Called at render time so Build itself stays deterministic.
func (g *Graph) Stamp(repoRoot string) {
	g.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	g.Revision = gitShortRev(repoRoot)
}

// gitShortRev returns the abbreviated HEAD commit of the repo at root, or "" when it
// cannot be resolved (not a repo, no git, detached/empty). Best-effort provenance —
// never fatal.
func gitShortRev(root string) string {
	out, err := exec.Command("git", "-C", root, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// underTests reports whether a repo-relative (forward-slashed) package dir is test
// scaffolding by location — the top-level tests/ tree.
func underTests(dir string) bool {
	return dir == "tests" || strings.HasPrefix(dir, "tests/")
}

// pkgAgg accumulates one package directory's facts across its files.
type pkgAgg struct {
	dir      string
	pkg      string
	files    int
	public   int
	intra    map[string]bool // imported intra-module import paths
	external map[string]bool // imported non-module paths
}

// Build walks the Go module rooted at root and returns its package-level graph with
// test scaffolding excluded (the default). Equivalent to BuildWith(root, Options{}).
func Build(root string) (*Graph, error) { return BuildWith(root, Options{}) }

// BuildWith builds the package-level graph for the project rooted at root, dispatching
// by repo kind so the graph is language-agnostic (epic:codegraph-portable). A Go module
// (go.mod) yields the import graph; a .NET solution/projects (.sln/.csproj) yields the
// project-reference graph. Both emit the SAME canonical Graph schema, so the viewer, the
// query layer (deps/rdeps/cycles), and MCP consumers are language-neutral. An
// unrecognised tree is a clear error rather than a Go-only failure.
func BuildWith(root string, opts Options) (*Graph, error) {
	if fileExists(filepath.Join(root, "go.mod")) {
		return buildGo(root, opts)
	}
	if hasCSharpProjects(root) {
		return buildCSharp(root)
	}
	return nil, fmt.Errorf("codegraph: no recognised project at %s (need a go.mod, or a .sln/.csproj)", root)
}

// fileExists reports whether path names an existing regular file.
func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

// buildGo walks the Go module rooted at root and returns its package-level graph.
// The module path is read from go.mod; an import is an intra-module edge when it
// carries the module prefix. Unparseable files are skipped (non-fatal) so one bad
// file never sinks the whole graph.
//
// Production structure comes from non-`_test.go` files only (nodes, edges, counts).
// `_test.go` files contribute no nodes/edges; their intra-module imports are tracked
// separately so test-support packages can be identified. Unless opts.IncludeTests,
// test-support packages (under tests/, or imported only from tests and never from
// production code) and any edge touching them are dropped from the result.
func buildGo(root string, opts Options) (*Graph, error) {
	module, err := modulePath(root)
	if err != nil {
		return nil, err
	}

	aggs := map[string]*pkgAgg{}      // production packages, keyed by repo-relative dir
	testImported := map[string]bool{} // intra-module paths imported from _test.go files
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") {
			return nil
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil // unreadable — skip
		}
		pkg, imports, public, perr := parseGoFile(src)
		if perr != nil {
			return nil // unparseable — skip, non-fatal
		}
		if strings.HasSuffix(name, "_test.go") {
			// Test files build no nodes/edges; record only their intra-module
			// imports so a package reached solely from tests can be classified.
			for _, imp := range imports {
				if imp == module || strings.HasPrefix(imp, module+"/") {
					testImported[imp] = true
				}
			}
			return nil
		}
		dir := filepath.ToSlash(relDir(root, path))
		a := aggs[dir]
		if a == nil {
			a = &pkgAgg{dir: dir, intra: map[string]bool{}, external: map[string]bool{}}
			aggs[dir] = a
		}
		if a.pkg == "" {
			a.pkg = pkg
		}
		a.files++
		a.public += public
		for _, imp := range imports {
			if imp == module || strings.HasPrefix(imp, module+"/") {
				a.intra[imp] = true
			} else {
				a.external[imp] = true
			}
		}
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("codegraph: walk: %w", walkErr)
	}

	g := &Graph{Module: module, RepoRoot: root}
	for _, a := range aggs {
		g.Nodes = append(g.Nodes, Node{
			ImportPath:    dirImportPath(module, a.dir),
			Dir:           a.dir,
			Package:       a.pkg,
			Files:         a.files,
			PublicSymbols: a.public,
			ExternalDeps:  len(a.external),
		})
	}
	// Edges are intra-module imports, deduped; an import to a package that has no
	// indexed files (e.g. a generated or build-excluded dir) is still a real edge.
	seen := map[string]bool{}
	for _, a := range aggs {
		from := dirImportPath(module, a.dir)
		for to := range a.intra {
			if to == from {
				continue
			}
			key := from + "\x00" + to
			if seen[key] {
				continue
			}
			seen[key] = true
			g.Edges = append(g.Edges, Edge{From: from, To: to})
		}
	}

	if !opts.IncludeTests {
		filterTestSupport(g, testImported)
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

// filterTestSupport drops test-scaffolding packages (and edges touching them) in
// place. A node is test-support when its dir is under tests/, OR it is imported from
// a _test.go file (testImported) but never from production code (no production edge
// points to it). Production entry points imported by nobody are NOT test-support.
func filterTestSupport(g *Graph, testImported map[string]bool) {
	prodImported := map[string]bool{}
	for _, e := range g.Edges {
		prodImported[e.To] = true
	}
	drop := map[string]bool{}
	kept := g.Nodes[:0]
	for _, n := range g.Nodes {
		if underTests(n.Dir) || (testImported[n.ImportPath] && !prodImported[n.ImportPath]) {
			drop[n.ImportPath] = true
			continue
		}
		kept = append(kept, n)
	}
	g.Nodes = kept
	keptEdges := g.Edges[:0]
	for _, e := range g.Edges {
		if drop[e.From] || drop[e.To] {
			continue
		}
		keptEdges = append(keptEdges, e)
	}
	g.Edges = keptEdges
}

// parseGoFile returns a file's package name, its import paths, and its count of
// exported top-level declarations (the public surface contribution).
func parseGoFile(src []byte) (pkg string, imports []string, public int, err error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, parser.SkipObjectResolution)
	if err != nil {
		return "", nil, 0, err
	}
	pkg = f.Name.Name
	for _, imp := range f.Imports {
		imports = append(imports, strings.Trim(imp.Path.Value, `"`))
	}
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name.IsExported() {
				public++
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name.IsExported() {
						public++
					}
				case *ast.ValueSpec:
					for _, n := range s.Names {
						if n.IsExported() {
							public++
						}
					}
				}
			}
		}
	}
	return pkg, imports, public, nil
}

// modulePath reads the module path from the repo's go.mod.
func modulePath(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("codegraph: read go.mod: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module")), nil
		}
	}
	return "", fmt.Errorf("codegraph: no module directive in go.mod")
}

// dirImportPath maps a repo-relative package dir to its module-qualified import
// path. The module root dir (".") is the module path itself.
func dirImportPath(module, dir string) string {
	if dir == "." || dir == "" {
		return module
	}
	return module + "/" + dir
}

func relDir(root, path string) string {
	rel, err := filepath.Rel(root, filepath.Dir(path))
	if err != nil {
		return filepath.Dir(path)
	}
	return rel
}
