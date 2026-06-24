// JGF (JSON Graph Format, https://jsongraphformat.info) is the canonical, interoperable
// codegraph format (epic:codegraph-portable). A published spec rather than satellites'
// proprietary shape, it is JSON-native and language-neutral, so a 3rd-party tool can
// produce a conformant codegraph and the portal renders it. The published `type:codegraph`
// document declares `format:jgf-v1` and carries this JSON in its ```codegraph block.
//
// Profile satellites emits/accepts (a subset of JGF, single-graph):
//
//	{ "graph": {
//	    "directed": true,
//	    "type": "code-dependency",
//	    "label": "<module>",
//	    "metadata": { "generatedAt", "revision", "repoRoot", "format": "jgf-v1" },
//	    "nodes": { "<id>": { "label": "<short>", "metadata": { package, files, publicSymbols, externalDeps } } },
//	    "edges": [ { "source": "<id>", "target": "<id>", "relation": "depends-on" } ]
//	} }
//
// Language-specific counts live in node metadata (advisory), so C# and other producers
// conform without faking Go-only structural fields.
package codegraph

import (
	"encoding/json"
	"io"
)

// JGFFormatID is the format identifier the published document declares (the `format:<id>`
// tag) and the portal gates on. Bump the version when the profile changes incompatibly.
const JGFFormatID = "jgf-v1"

// JGFDocument is the top-level JSON Graph Format envelope (single-graph form).
type JGFDocument struct {
	Graph JGFGraph `json:"graph"`
}

// JGFGraph is one graph: directedness, a free `type`/`label`, graph-level metadata, the
// node map (keyed by id), and the edge list.
type JGFGraph struct {
	Directed bool               `json:"directed"`
	Type     string             `json:"type"`
	Label    string             `json:"label"`
	Metadata map[string]any     `json:"metadata,omitempty"`
	Nodes    map[string]JGFNode `json:"nodes"`
	Edges    []JGFEdge          `json:"edges"`
}

// JGFNode is one node's display label + advisory metadata (the id is the map key).
type JGFNode struct {
	Label    string         `json:"label"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// JGFEdge is one directed edge, source/target referencing node ids, with a relation kind.
type JGFEdge struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	Relation string `json:"relation,omitempty"`
}

// ToJGF projects the native Graph onto the canonical JGF profile. Node ids are the native
// ImportPath (module-qualified import path for Go, project/assembly name for C#); the label
// is the repo-relative short form. Determinism: the node map marshals with sorted keys
// (encoding/json), and Edges is already sorted by Build.
func (g *Graph) ToJGF() *JGFDocument {
	gm := map[string]any{"format": JGFFormatID}
	if g.GeneratedAt != "" {
		gm["generatedAt"] = g.GeneratedAt
	}
	if g.Revision != "" {
		gm["revision"] = g.Revision
	}
	if g.RepoRoot != "" {
		gm["repoRoot"] = g.RepoRoot
	}

	nodes := make(map[string]JGFNode, len(g.Nodes))
	for _, n := range g.Nodes {
		nodes[n.ImportPath] = JGFNode{
			Label: g.Short(n.ImportPath),
			Metadata: map[string]any{
				"package":       n.Package,
				"files":         n.Files,
				"publicSymbols": n.PublicSymbols,
				"externalDeps":  n.ExternalDeps,
			},
		}
	}
	edges := make([]JGFEdge, 0, len(g.Edges))
	for _, e := range g.Edges {
		edges = append(edges, JGFEdge{Source: e.From, Target: e.To, Relation: "depends-on"})
	}

	return &JGFDocument{Graph: JGFGraph{
		Directed: true,
		Type:     "code-dependency",
		Label:    g.Module,
		Metadata: gm,
		Nodes:    nodes,
		Edges:    edges,
	}}
}

// RenderJGF writes the graph as stable, indented JGF JSON — the canonical machine form the
// published codegraph document carries and the portal/viewer consume.
func (g *Graph) RenderJGF(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(g.ToJGF())
}
