package codegraph

import (
	"sort"
	"strings"
)

// query.go adds the focused-question layer over the package graph (A1,
// epic:codegraph-usability): resolve a package reference, list a package's direct
// in/out edges, compute forward/reverse transitive closures (depends-on /
// blast-radius), and detect import cycles. All are PURE functions over an
// already-built *Graph — no I/O, no new build path, no index.db change. The binary
// emits these facts; rendering/opinion stays in the CLI + substrate.

// PackageFocus is one package's direct neighbourhood: the intra-module packages it
// imports (DependsOn, out-edges) and the ones that import it (DependedOnBy,
// in-edges). Both are full module-qualified import paths, sorted.
type PackageFocus struct {
	Package      string   `json:"package"`
	DependsOn    []string `json:"depends_on"`
	DependedOnBy []string `json:"depended_on_by"`
}

// ResolvePackage maps a user-supplied package reference to a canonical
// module-qualified import path present in the graph. It accepts, in order: an exact
// import path, a repo-relative dir (e.g. "internal/codeindex"), the module-qualified
// form of that dir, or an UNAMBIGUOUS "/"-suffix match. An absent or ambiguous
// reference returns ok=false. The reference is matched against every import path the
// graph knows — node packages AND edge endpoints — so a package that is only
// imported (no indexed files of its own) still resolves as an rdeps/deps target.
func ResolvePackage(g *Graph, ref string) (string, bool) {
	ref = strings.Trim(strings.TrimSpace(ref), "/")
	if ref == "" {
		return "", false
	}
	known := allImportPaths(g)
	knownSet := make(map[string]bool, len(known))
	for _, p := range known {
		knownSet[p] = true
	}
	// 1. exact import path.
	if knownSet[ref] {
		return ref, true
	}
	// 2. exact repo-relative dir (nodes carry Dir).
	for _, n := range g.Nodes {
		if n.Dir == ref {
			return n.ImportPath, true
		}
	}
	// 3. module-qualified form of a repo-relative dir.
	if q := dirImportPath(g.Module, ref); knownSet[q] {
		return q, true
	}
	// 4. unambiguous "/"-suffix match.
	var matches []string
	for _, p := range known {
		if strings.HasSuffix(p, "/"+ref) {
			matches = append(matches, p)
		}
	}
	if len(matches) == 1 {
		return matches[0], true
	}
	return "", false
}

// Package returns pkg's direct in/out neighbourhood. pkg must be a canonical import
// path (resolve via ResolvePackage first). The slices are always non-nil (empty, not
// null, so the JSON form is stable) and sorted.
func Package(g *Graph, pkg string) PackageFocus {
	f := PackageFocus{Package: pkg, DependsOn: []string{}, DependedOnBy: []string{}}
	for _, e := range g.Edges {
		if e.From == pkg {
			f.DependsOn = append(f.DependsOn, e.To)
		}
		if e.To == pkg {
			f.DependedOnBy = append(f.DependedOnBy, e.From)
		}
	}
	sort.Strings(f.DependsOn)
	sort.Strings(f.DependedOnBy)
	return f
}

// Deps returns the forward transitive closure of pkg — every intra-module package
// pkg pulls in, directly or transitively (what it depends on). The seed is excluded;
// the result is sorted and always non-nil.
func Deps(g *Graph, pkg string) []string {
	return closure(forwardAdj(g), pkg)
}

// RDeps returns the reverse transitive closure of pkg — every intra-module package
// that imports pkg, directly or transitively (the blast radius: who breaks if pkg
// changes). The seed is excluded; the result is sorted and always non-nil.
func RDeps(g *Graph, pkg string) []string {
	return closure(reverseAdj(g), pkg)
}

// Cycles detects intra-module import cycles via a coloured DFS (white/grey/black)
// over the forward adjacency. Each cycle is returned as the ordered list of its
// member packages, rotated so the lexicographically smallest member is first
// (canonical form) and deduped across DFS roots. The whole result is sorted for
// determinism. A Go module that compiles is acyclic, so this is also a structural
// check — an empty result is the healthy case.
func Cycles(g *Graph) [][]string {
	adj := forwardAdj(g)
	for k := range adj {
		sort.Strings(adj[k])
	}
	const (
		white = 0
		grey  = 1
		black = 2
	)
	color := map[string]int{}
	var stack []string
	var cycles [][]string
	seen := map[string]bool{}

	var dfs func(node string)
	dfs = func(node string) {
		color[node] = grey
		stack = append(stack, node)
		for _, nb := range adj[node] {
			switch color[nb] {
			case white:
				dfs(nb)
			case grey:
				cyc := normalizeCycle(extractCycle(stack, nb))
				key := strings.Join(cyc, "\x00")
				if !seen[key] {
					seen[key] = true
					cycles = append(cycles, cyc)
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[node] = black
	}

	for _, n := range allImportPaths(g) { // deterministic root order
		if color[n] == white {
			dfs(n)
		}
	}
	sort.Slice(cycles, func(i, j int) bool {
		return strings.Join(cycles[i], "\x00") < strings.Join(cycles[j], "\x00")
	})
	return cycles
}

// Short trims the module prefix so a path reads in repo-relative terms (the module
// root prints as "."). The exported counterpart of render.go's short, for the CLI
// query renderers.
func (g *Graph) Short(importPath string) string { return short(g.Module, importPath) }

// --- internals ---

// allImportPaths is the sorted union of every import path the graph references —
// node packages plus edge endpoints — so a target that is only an edge endpoint
// still resolves.
func allImportPaths(g *Graph) []string {
	set := map[string]bool{}
	for _, n := range g.Nodes {
		set[n.ImportPath] = true
	}
	for _, e := range g.Edges {
		set[e.From] = true
		set[e.To] = true
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func forwardAdj(g *Graph) map[string][]string {
	m := map[string][]string{}
	for _, e := range g.Edges {
		m[e.From] = append(m[e.From], e.To)
	}
	return m
}

func reverseAdj(g *Graph) map[string][]string {
	m := map[string][]string{}
	for _, e := range g.Edges {
		m[e.To] = append(m[e.To], e.From)
	}
	return m
}

// closure returns the BFS transitive closure of seed over adj, excluding the seed
// itself. Always non-nil and sorted.
func closure(adj map[string][]string, seed string) []string {
	seen := map[string]bool{}
	queue := []string{seed}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, nb := range adj[cur] {
			if nb == seed || seen[nb] {
				continue
			}
			seen[nb] = true
			queue = append(queue, nb)
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// extractCycle returns the segment of the DFS stack from the first occurrence of
// start to the top — the member list of the back-edge cycle.
func extractCycle(stack []string, start string) []string {
	for i, n := range stack {
		if n == start {
			cyc := make([]string, len(stack)-i)
			copy(cyc, stack[i:])
			return cyc
		}
	}
	return append([]string(nil), stack...) // unreachable: start is always on the stack
}

// normalizeCycle rotates a cycle's member list so its lexicographically smallest
// member is first — a canonical form so the same cycle found from different DFS
// roots dedupes to one entry.
func normalizeCycle(cyc []string) []string {
	if len(cyc) == 0 {
		return cyc
	}
	min := 0
	for i := 1; i < len(cyc); i++ {
		if cyc[i] < cyc[min] {
			min = i
		}
	}
	out := make([]string, 0, len(cyc))
	out = append(out, cyc[min:]...)
	out = append(out, cyc[:min]...)
	return out
}
