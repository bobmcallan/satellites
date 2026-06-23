// `satellites code graph` — the high-level package code graph (C1,
// epic:phases-task-outputs). It walks the Go module and emits package nodes (public
// surface) joined by intra-module import edges — the package-level dependency map a
// codegraph document renders, "not function contents". On-demand like `code map`:
// a go/ast walk, no stored index, no schema change.
//
// Without a query flag it prints the whole graph (human) or --json the stable
// machine form the C2 codegraph skill consumes. The A1 query layer
// (epic:codegraph-usability) adds focused questions over the same in-memory graph:
// --package (direct in/out edges), --deps / --rdeps (forward / reverse transitive
// closure — blast radius), and --cycles (import-cycle detection). Each composes with
// --json.
package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/bobmcallan/satellites/internal/codegraph"
	"github.com/spf13/cobra"
)

// codeGraphQuery captures the at-most-one focused query selected by flags. An empty
// value means "no query" — the full-graph dump (today's behaviour, unchanged).
type codeGraphQuery struct {
	asJSON       bool
	includeTests bool   // --include-tests: keep test-support packages
	pkg          string // --package: direct in/out edges
	deps         string // --deps: forward transitive closure
	rdeps        string // --rdeps: reverse transitive closure (blast radius)
	cycles       bool   // --cycles: import-cycle detection
}

func newCodeGraphCmd() *cobra.Command {
	var graphConfig string
	var q codeGraphQuery
	cmd := &cobra.Command{
		Use:   "graph",
		Short: "High-level package code graph: nodes, edges, and focused queries (deps / rdeps / cycles)",
		Long: `graph walks the Go module and builds the package-level code graph —
one node per package (with its public-surface count) and one edge per intra-module
import (package A imports package B). It is the high-level structure of the
codebase ("not function contents"), the data a codegraph document renders as a
dependency diagram. On-demand like ` + "`code map`" + ` — a go/ast walk, no stored index.

With no query flag it prints the whole graph. The query flags answer focused
questions over the same graph (each composes with --json; at most one at a time):

  --package <pkg>   the package's direct dependencies (out-edges) and dependents (in-edges)
  --deps <pkg>      forward transitive closure — everything <pkg> pulls in
  --rdeps <pkg>     reverse transitive closure — the blast radius (who imports <pkg>, transitively)
  --cycles          detect intra-module import cycles (empty on a module that compiles)

<pkg> accepts a repo-relative dir (internal/codeindex) or a full import path.

--json emits the stable machine form. For the full graph:
  { "module":…, "repo_root":…, "nodes":[ {import_path,dir,package,files,public_symbols,external_deps} ], "edges":[ {from,to} ] }
For --package:  { "package":…, "depends_on":[…], "depended_on_by":[…] }
For --deps / --rdeps:  { "package":…, "query":"deps|rdeps", "closure":[…] }
For --cycles:  { "cycles": [ [pkg,…], … ] }`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, _ := resolveCodeIndex(graphConfig)
			return runCodeGraph(cmd.OutOrStdout(), repoRoot, q)
		},
	}
	cmd.Flags().StringVar(&graphConfig, "config", "", "Path to satellites.toml (resolves repo root; defaults to walk-up from CWD).")
	cmd.Flags().BoolVar(&q.asJSON, "json", false, "Emit the result as JSON (stable machine form) instead of the human summary.")
	cmd.Flags().StringVar(&q.pkg, "package", "", "Focus on one package: its direct dependencies and dependents.")
	cmd.Flags().StringVar(&q.deps, "deps", "", "Forward transitive closure — everything the package pulls in.")
	cmd.Flags().StringVar(&q.rdeps, "rdeps", "", "Reverse transitive closure (blast radius) — who imports the package, transitively.")
	cmd.Flags().BoolVar(&q.cycles, "cycles", false, "Detect intra-module import cycles.")
	cmd.Flags().BoolVar(&q.includeTests, "include-tests", false, "Keep test-support packages (under tests/, or imported only from _test.go) that are excluded by default.")
	return cmd
}

func runCodeGraph(out io.Writer, repoRoot string, q codeGraphQuery) error {
	// At most one query flag may be active — they answer different questions over
	// the same graph and combining them is a usage error.
	selected := 0
	for _, on := range []bool{q.pkg != "", q.deps != "", q.rdeps != "", q.cycles} {
		if on {
			selected++
		}
	}
	if selected > 1 {
		return fmt.Errorf("code graph: choose at most one of --package, --deps, --rdeps, --cycles")
	}

	g, err := codegraph.BuildWith(repoRoot, codegraph.Options{IncludeTests: q.includeTests})
	if err != nil {
		return fmt.Errorf("code graph: %w", err)
	}
	g.Stamp(repoRoot) // provenance on the full-graph snapshot

	switch {
	case q.cycles:
		return renderCycles(out, g, q.asJSON)
	case q.pkg != "":
		return renderPackage(out, g, q.pkg, q.asJSON)
	case q.deps != "":
		return renderClosure(out, g, q.deps, "deps", q.asJSON)
	case q.rdeps != "":
		return renderClosure(out, g, q.rdeps, "rdeps", q.asJSON)
	default:
		if q.asJSON {
			return g.RenderJSON(out)
		}
		g.Render(out)
		return nil
	}
}

// writeJSON emits v as stable, indented, non-HTML-escaped JSON — matching
// Graph.RenderJSON so every code-graph JSON form reads the same.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// resolveOrErr maps a package reference to its canonical import path, returning a
// clear naming error when it is absent or ambiguous.
func resolveOrErr(g *codegraph.Graph, ref string) (string, error) {
	pkg, ok := codegraph.ResolvePackage(g, ref)
	if !ok {
		return "", fmt.Errorf("code graph: package %q not found in the module graph (use a repo-relative dir or a full import path)", ref)
	}
	return pkg, nil
}

func renderPackage(out io.Writer, g *codegraph.Graph, ref string, asJSON bool) error {
	pkg, err := resolveOrErr(g, ref)
	if err != nil {
		return err
	}
	f := codegraph.Package(g, pkg)
	if asJSON {
		return writeJSON(out, f)
	}
	fmt.Fprintf(out, "code graph — package %s\n", g.Short(pkg))
	fmt.Fprintf(out, "  depends on (%d):\n", len(f.DependsOn))
	for _, p := range f.DependsOn {
		fmt.Fprintf(out, "    - %s\n", g.Short(p))
	}
	fmt.Fprintf(out, "  depended on by (%d):\n", len(f.DependedOnBy))
	for _, p := range f.DependedOnBy {
		fmt.Fprintf(out, "    - %s\n", g.Short(p))
	}
	return nil
}

// closureResult is the stable JSON shape for --deps / --rdeps.
type closureResult struct {
	Package string   `json:"package"`
	Query   string   `json:"query"`
	Closure []string `json:"closure"`
}

func renderClosure(out io.Writer, g *codegraph.Graph, ref, kind string, asJSON bool) error {
	pkg, err := resolveOrErr(g, ref)
	if err != nil {
		return err
	}
	var set []string
	if kind == "rdeps" {
		set = codegraph.RDeps(g, pkg)
	} else {
		set = codegraph.Deps(g, pkg)
	}
	if asJSON {
		return writeJSON(out, closureResult{Package: pkg, Query: kind, Closure: set})
	}
	label := "depends on (forward)"
	if kind == "rdeps" {
		label = "blast radius (reverse)"
	}
	fmt.Fprintf(out, "code graph — %s of %s: %d package(s)\n", label, g.Short(pkg), len(set))
	for _, p := range set {
		fmt.Fprintf(out, "  - %s\n", g.Short(p))
	}
	return nil
}

// cyclesResult is the stable JSON shape for --cycles.
type cyclesResult struct {
	Cycles [][]string `json:"cycles"`
}

func renderCycles(out io.Writer, g *codegraph.Graph, asJSON bool) error {
	cycles := codegraph.Cycles(g)
	if asJSON {
		if cycles == nil {
			cycles = [][]string{}
		}
		return writeJSON(out, cyclesResult{Cycles: cycles})
	}
	if len(cycles) == 0 {
		fmt.Fprintln(out, "code graph — cycles: none")
		return nil
	}
	fmt.Fprintf(out, "code graph — cycles: %d\n", len(cycles))
	for i, cyc := range cycles {
		shorts := make([]string, len(cyc))
		for j, p := range cyc {
			shorts[j] = g.Short(p)
		}
		// Close the loop visually: a → b → a.
		fmt.Fprintf(out, "  %d. ", i+1)
		for _, s := range shorts {
			fmt.Fprintf(out, "%s → ", s)
		}
		fmt.Fprintf(out, "%s\n", shorts[0])
	}
	return nil
}
