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
	"path/filepath"
	"sort"
	"strings"
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

// Graph is the full package-level result for one module.
type Graph struct {
	Module   string `json:"module"`
	RepoRoot string `json:"repo_root"`
	Nodes    []Node `json:"nodes"`
	Edges    []Edge `json:"edges"`
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

// Build walks the Go module rooted at root and returns its package-level graph.
// The module path is read from go.mod; an import is an intra-module edge when it
// carries the module prefix. Unparseable files are skipped (non-fatal) so one bad
// file never sinks the whole graph.
func Build(root string) (*Graph, error) {
	module, err := modulePath(root)
	if err != nil {
		return nil, err
	}

	aggs := map[string]*pkgAgg{} // keyed by repo-relative dir
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
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
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

	sort.Slice(g.Nodes, func(i, j int) bool { return g.Nodes[i].ImportPath < g.Nodes[j].ImportPath })
	sort.Slice(g.Edges, func(i, j int) bool {
		if g.Edges[i].From != g.Edges[j].From {
			return g.Edges[i].From < g.Edges[j].From
		}
		return g.Edges[i].To < g.Edges[j].To
	})
	return g, nil
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
