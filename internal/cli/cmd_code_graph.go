// `satellites code graph` — the high-level package code graph (C1,
// epic:phases-task-outputs). It walks the Go module and emits package nodes (public
// surface) joined by intra-module import edges — the package-level dependency map a
// codegraph document renders, "not function contents". On-demand like `code map`:
// a go/ast walk, no stored index, no schema change.
//
// --json emits the stable machine form the C2 codegraph skill consumes; without it,
// a human-readable package + dependency summary.
package cli

import (
	"fmt"
	"io"

	"github.com/bobmcallan/satellites/internal/codegraph"
	"github.com/spf13/cobra"
)

func newCodeGraphCmd() *cobra.Command {
	var graphConfig string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "graph",
		Short: "High-level package code graph: package nodes → intra-module import edges",
		Long: `graph walks the Go module and builds the package-level code graph —
one node per package (with its public-surface count) and one edge per intra-module
import (package A imports package B). It is the high-level structure of the
codebase ("not function contents"), the data a codegraph document renders as a
dependency diagram. On-demand like ` + "`code map`" + ` — a go/ast walk, no stored index.

--json emits the stable machine form:
  {
    "module":    "<module path>",
    "repo_root": "<abs path>",
    "nodes": [ {
        "import_path":    "<module-qualified path>",
        "dir":            "<repo-rel dir>",
        "package":        "<package name>",
        "files":          <int>,
        "public_symbols": <int>,   // exported top-level declarations
        "external_deps":  <int>    // distinct non-module imports
    } ],
    "edges": [ {"from": "<import path>", "to": "<import path>"} ]  // intra-module imports
  }`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, _ := resolveCodeIndex(graphConfig)
			return runCodeGraph(cmd.OutOrStdout(), repoRoot, asJSON)
		},
	}
	cmd.Flags().StringVar(&graphConfig, "config", "", "Path to satellites.toml (resolves repo root; defaults to walk-up from CWD).")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Emit the graph as JSON (stable machine form) instead of the human summary.")
	return cmd
}

func runCodeGraph(out io.Writer, repoRoot string, asJSON bool) error {
	g, err := codegraph.Build(repoRoot)
	if err != nil {
		return fmt.Errorf("code graph: %w", err)
	}
	if asJSON {
		return g.RenderJSON(out)
	}
	g.Render(out)
	return nil
}
