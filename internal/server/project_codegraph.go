package server

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"strings"

	"github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/codegraph"
	"github.com/bobmcallan/satellites/internal/verb"
)

// project_codegraph.go surfaces a project's published codegraph (B2,
// epic:codegraph-usability) on the project page. The `type:codegraph` document
// (published by the Codegraph task, B1) is NOT a `type:document` row, so it does not
// appear in the documents panel — this fetches it directly and renders its body into
// a dedicated card, where codegraph-init.js turns the embedded `language-codegraph`
// JSON block into an interactive graph.
//
// The document carries ONE canonical representation — the `codegraph` fenced JSON block
// (epic:codegraph-portable, story 3). The two derived views come from that single source:
// the interactive diagram is rendered client-side by codegraph-init.js (cytoscape), and
// the package table is rendered HERE, server-side, from the same JSON — so the table is
// available without JavaScript. Read-only and best-effort: any miss/error yields an empty
// card (graceful absence), never a blanked page.

// gatherCodegraph resolves the newest `type:codegraph` document for a project and returns
// its body rendered as safe markdown (carrying the `codegraph` JSON block the viewer
// consumes) plus the package table derived server-side from that same block. ok=false on a
// missing document, empty list, or any verb error — the caller renders no card. table is
// empty when the body carries no parseable `codegraph` block (the card still renders the
// diagram source).
func gatherCodegraph(ctx context.Context, projectID string) (body, table template.HTML, ok bool) {
	listReq, _ := json.Marshal(verb.DocumentListRequest{
		Type:      "document",
		ProjectID: projectID,
		Tags:      []string{"type:codegraph"},
		Limit:     50,
	})
	raw, err := verb.Dispatch(ctx, "document_list", listReq)
	if err != nil {
		arbor.WarnCtx(ctx, "project_codegraph: list", "id", projectID, "err", err)
		return "", "", false
	}
	var resp verb.DocumentListResponse
	if err := json.Unmarshal(raw, &resp); err != nil || len(resp.Items) == 0 {
		return "", "", false
	}
	// Newest by updated_at — the current-state snapshot (the task overwrites one doc,
	// but guard against duplicates by picking the freshest).
	newest := resp.Items[0]
	for _, d := range resp.Items[1:] {
		if d.UpdatedAt.After(newest.UpdatedAt) {
			newest = d
		}
	}
	docBody, err := dispatchStoryBody(ctx, newest.ID)
	if err != nil {
		arbor.WarnCtx(ctx, "project_codegraph: body", "id", newest.ID, "err", err)
		return "", "", false
	}
	if strings.TrimSpace(docBody) == "" {
		return "", "", false
	}
	tbl, _ := codegraphPackageTable(docBody)
	return renderMarkdown(docBody), tbl, true
}

// codegraphPackageTable derives the package table from the `codegraph` JSON block in a
// document body — the single canonical source. It returns ("", false) when no block is
// present or the JSON does not parse, so a malformed/empty body simply renders no table.
func codegraphPackageTable(body string) (template.HTML, bool) {
	block, ok := extractCodegraphBlock(body)
	if !ok {
		return "", false
	}
	var g codegraph.Graph
	if err := json.Unmarshal([]byte(block), &g); err != nil || len(g.Nodes) == 0 {
		return "", false
	}
	var b strings.Builder
	b.WriteString(`<table class="codegraph-packages" data-field="codegraph-packages">`)
	b.WriteString(`<thead><tr><th>package</th><th>name</th><th class="num">public</th>`)
	b.WriteString(`<th class="num">files</th><th class="num">ext deps</th></tr></thead><tbody>`)
	for _, n := range g.Nodes {
		fmt.Fprintf(&b,
			`<tr><td><code>%s</code></td><td>%s</td><td class="num">%d</td><td class="num">%d</td><td class="num">%d</td></tr>`,
			template.HTMLEscapeString(shortImportPath(g.Module, n.ImportPath)),
			template.HTMLEscapeString(n.Package),
			n.PublicSymbols, n.Files, n.ExternalDeps,
		)
	}
	b.WriteString(`</tbody></table>`)
	return template.HTML(b.String()), true
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

// shortImportPath trims the module prefix so a package reads in repo-relative terms (the
// module root renders as "."), mirroring the CLI's human form and codegraph-init.js.
func shortImportPath(module, p string) string {
	if p == module {
		return "."
	}
	if strings.HasPrefix(p, module+"/") {
		return p[len(module)+1:]
	}
	return p
}
