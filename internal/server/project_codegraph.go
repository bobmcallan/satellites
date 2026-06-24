package server

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"sort"
	"strings"

	"github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/codegraph"
	"github.com/bobmcallan/satellites/internal/verb"
)

// project_codegraph.go surfaces a project's published codegraph on the project page. The
// `type:codegraph` document declares its format via a `format:<id>` tag
// (epic:codegraph-portable); the portal renders it ONLY when that format is acceptable —
// a recognized format renders the interactive diagram (codegraph-init.js, cytoscape) plus
// a server-rendered package table derived from the same canonical JSON, and an unknown or
// missing format renders an explicit "unsupported format" state rather than a blank card.
// The canonical format is JGF (JSON Graph Format) — a common, interoperable spec a
// 3rd-party tool can produce. Read-only and best-effort: any miss/error yields no card.

// acceptableCodegraphFormats is the set of `format:<id>` ids the portal can render. It is
// the extension point: adding DOT/GraphML/cytoscape-json support means adding its id here
// (and the matching parse/render path). Today only JGF is accepted.
var acceptableCodegraphFormats = map[string]bool{
	codegraph.JGFFormatID: true, // "jgf-v1"
}

// codegraphView is the resolved state of a project's published codegraph for the template.
type codegraphView struct {
	Body       template.HTML // the ```codegraph diagram source, rendered HTML — set only when Acceptable
	Table      template.HTML // the package table derived from the JGF — set only when Acceptable
	Format     string        // the declared format id (from the format:<id> tag), "" if none
	Acceptable bool          // the declared format is recognized and renderable
	Found      bool          // a type:codegraph document exists for the project
}

// gatherCodegraph resolves the newest `type:codegraph` document for a project and decides
// how the portal renders it: it reads the document's declared `format:<id>` tag and gates
// on the accept-set. When acceptable, it returns the diagram source markdown + the package
// table derived from the JGF; when not, it returns an unsupported-format view (Found=true,
// Acceptable=false) the template renders as an explicit notice. Found=false means no card.
func gatherCodegraph(ctx context.Context, projectID string) codegraphView {
	listReq, _ := json.Marshal(verb.DocumentListRequest{
		Type:      "document",
		ProjectID: projectID,
		Tags:      []string{"type:codegraph"},
		Limit:     50,
	})
	raw, err := verb.Dispatch(ctx, "document_list", listReq)
	if err != nil {
		arbor.WarnCtx(ctx, "project_codegraph: list", "id", projectID, "err", err)
		return codegraphView{}
	}
	var resp verb.DocumentListResponse
	if err := json.Unmarshal(raw, &resp); err != nil || len(resp.Items) == 0 {
		return codegraphView{}
	}
	// Newest by updated_at — the current-state snapshot.
	newest := resp.Items[0]
	for _, d := range resp.Items[1:] {
		if d.UpdatedAt.After(newest.UpdatedAt) {
			newest = d
		}
	}

	view := codegraphView{Found: true, Format: codegraphFormatTag(newest.Tags)}
	view.Acceptable = acceptableCodegraphFormats[view.Format]
	if !view.Acceptable {
		// A declared-but-unknown or undeclared format: the card renders an explicit notice;
		// we do not attempt to parse or render an unrecognized body.
		return view
	}

	docBody, err := dispatchStoryBody(ctx, newest.ID)
	if err != nil {
		arbor.WarnCtx(ctx, "project_codegraph: body", "id", newest.ID, "err", err)
		view.Found = false // body unreadable — render no card rather than a broken one
		return view
	}
	if strings.TrimSpace(docBody) == "" {
		view.Found = false
		return view
	}
	view.Body = renderMarkdown(docBody)
	view.Table, _ = codegraphPackageTable(docBody)
	return view
}

// codegraphFormatTag returns the declared format id — the value of the `format:<id>` tag —
// or "" when no such tag is present.
func codegraphFormatTag(tags []string) string {
	for _, t := range tags {
		if v, ok := strings.CutPrefix(t, "format:"); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// codegraphPackageTable derives the package table from the JGF `codegraph` block in a
// document body — the single canonical source the diagram also reads. Returns ("", false)
// when no block is present or the JGF does not parse, so a malformed body renders no table.
func codegraphPackageTable(body string) (template.HTML, bool) {
	block, ok := extractCodegraphBlock(body)
	if !ok {
		return "", false
	}
	var doc codegraph.JGFDocument
	if err := json.Unmarshal([]byte(block), &doc); err != nil || len(doc.Graph.Nodes) == 0 {
		return "", false
	}
	ids := make([]string, 0, len(doc.Graph.Nodes))
	for id := range doc.Graph.Nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids) // stable order — JGF nodes is an unordered object map
	var b strings.Builder
	b.WriteString(`<table class="codegraph-packages" data-field="codegraph-packages">`)
	b.WriteString(`<thead><tr><th>package</th><th>name</th><th class="num">public</th>`)
	b.WriteString(`<th class="num">files</th><th class="num">ext deps</th></tr></thead><tbody>`)
	for _, id := range ids {
		n := doc.Graph.Nodes[id]
		label := n.Label
		if label == "" {
			label = id
		}
		fmt.Fprintf(&b,
			`<tr><td><code>%s</code></td><td>%s</td><td class="num">%d</td><td class="num">%d</td><td class="num">%d</td></tr>`,
			template.HTMLEscapeString(label),
			template.HTMLEscapeString(metaString(n.Metadata, "package")),
			metaInt(n.Metadata, "publicSymbols"), metaInt(n.Metadata, "files"), metaInt(n.Metadata, "externalDeps"),
		)
	}
	b.WriteString(`</tbody></table>`)
	return template.HTML(b.String()), true
}

// metaString reads a string field from a JGF metadata bag (absent → "").
func metaString(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// metaInt reads a numeric field from a JGF metadata bag (JSON numbers decode to float64;
// absent/non-numeric → 0).
func metaInt(m map[string]any, key string) int {
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	return 0
}

// extractCodegraphBlock returns the verbatim contents of the first ```codegraph fenced
// block in a markdown body. ok=false when no such block is present.
func extractCodegraphBlock(body string) (string, bool) {
	lines := strings.Split(body, "\n")
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "```codegraph" {
			var buf []string
			for j := i + 1; j < len(lines); j++ {
				if strings.HasPrefix(strings.TrimSpace(lines[j]), "```") {
					return strings.Join(buf, "\n"), true
				}
				buf = append(buf, lines[j])
			}
			return "", false // unterminated fence
		}
	}
	return "", false
}
