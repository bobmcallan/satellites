package codegraph

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

// RenderJSON writes the graph as stable, indented JSON — the machine form the C2
// codegraph skill consumes.
func (g *Graph) RenderJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(g)
}

// Render writes the human-readable summary: the module, a node count, and each
// package with its public surface and its intra-module dependencies (fan-out).
func (g *Graph) Render(w io.Writer) {
	fmt.Fprintf(w, "code graph — module %s: %d package(s), %d intra-module edge(s)\n",
		g.Module, len(g.Nodes), len(g.Edges))
	if g.GeneratedAt != "" || g.Revision != "" {
		rev := g.Revision
		if rev == "" {
			rev = "(unknown rev)"
		}
		fmt.Fprintf(w, "generated %s @ %s\n", g.GeneratedAt, rev)
	}
	fmt.Fprintln(w)

	deps := map[string][]string{}
	for _, e := range g.Edges {
		deps[e.From] = append(deps[e.From], short(g.Module, e.To))
	}
	for _, n := range g.Nodes {
		out := deps[n.ImportPath]
		sort.Strings(out)
		fmt.Fprintf(w, "  %-44s pkg=%-18s files=%-3d public=%-3d ext=%d\n",
			short(g.Module, n.ImportPath), n.Package, n.Files, n.PublicSymbols, n.ExternalDeps)
		if len(out) > 0 {
			fmt.Fprintf(w, "  %-44s → %v\n", "", out)
		}
	}
}

// short trims the module prefix so the human form reads in repo-relative terms;
// the module root prints as ".".
func short(module, importPath string) string {
	if importPath == module {
		return "."
	}
	if len(importPath) > len(module)+1 && importPath[:len(module)] == module {
		return importPath[len(module)+1:]
	}
	return importPath
}
