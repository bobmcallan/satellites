package server

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
)

// renderProjectDetail executes the real project_detail template with the given data
// and returns the HTML, so tests assert the server-rendered output deterministically
// (no browser, no session — the chromedp portal harness has a known session race).
func renderProjectDetail(t *testing.T, data projectDetailData) string {
	t.Helper()
	var buf bytes.Buffer
	if err := projectDetailTmpl.Execute(&buf, data); err != nil {
		t.Fatalf("execute project_detail template: %v", err)
	}
	return buf.String()
}

// TestProjectDetailCodegraphCardRenders is the B2 AC1/AC2 render test: when the
// project has a published codegraph (CodegraphHTML set), the project page carries the
// codegraph card with the embedded language-codegraph block the viewer consumes; when
// it has none, the card is absent (graceful absence) — and either way the page wires
// in codegraph-init.js so the lazy viewer is available.
func TestProjectDetailCodegraphCardRenders(t *testing.T) {
	cgBody := renderMarkdown("```codegraph\n{\"module\":\"m\",\"nodes\":[],\"edges\":[]}\n```")

	// AC1: with a codegraph, the card + the language-codegraph block render.
	withCard := renderProjectDetail(t, projectDetailData{
		Project:       projectRow{ID: "proj_x", Name: "x"},
		CodegraphHTML: cgBody,
	})
	if !strings.Contains(withCard, `data-section="project-codegraph"`) {
		t.Errorf("AC1: project page missing the codegraph card when a codegraph exists")
	}
	if !strings.Contains(withCard, "language-codegraph") {
		t.Errorf("AC1: codegraph card missing the embedded language-codegraph block")
	}
	if !strings.Contains(withCard, "/static/codegraph-init.js") {
		t.Errorf("AC1: project page does not wire in codegraph-init.js")
	}

	// AC2: with no codegraph, the card is absent — but the page still renders (graceful).
	noCard := renderProjectDetail(t, projectDetailData{
		Project:       projectRow{ID: "proj_y", Name: "y"},
		CodegraphHTML: template.HTML(""),
	})
	if strings.Contains(noCard, `data-section="project-codegraph"`) {
		t.Errorf("AC2: codegraph card rendered for a project with no codegraph document")
	}
	if !strings.Contains(noCard, `data-field="stories-table"`) {
		t.Errorf("AC2: project page did not render normally without a codegraph")
	}
}

// TestCodegraphPackageTable verifies the package table is derived server-side from the
// canonical `codegraph` JSON block (epic:codegraph-portable, story 3) — the single source
// the interactive diagram also reads. Short import paths, the per-node columns, and HTML
// escaping are all asserted; a body with no/!parseable block yields no table.
func TestCodegraphPackageTable(t *testing.T) {
	body := "# Codegraph — m\n\n## Graph data\n\n```codegraph\n" +
		`{"module":"m","nodes":[` +
		`{"import_path":"m","package":"main","files":2,"public_symbols":3,"external_deps":1},` +
		`{"import_path":"m/internal/cli","package":"cli","files":5,"public_symbols":7,"external_deps":4}` +
		`],"edges":[{"from":"m/internal/cli","to":"m"}]}` +
		"\n```\n"
	tbl, ok := codegraphPackageTable(body)
	if !ok {
		t.Fatalf("expected a table from a valid codegraph block")
	}
	html := string(tbl)
	for _, want := range []string{
		`class="codegraph-packages"`,
		`<td><code>.</code></td>`,            // module root short-name
		`<td><code>internal/cli</code></td>`, // module prefix trimmed
		`<td class="num">7</td>`,             // public_symbols of the cli node
	} {
		if !strings.Contains(html, want) {
			t.Errorf("table missing %q:\n%s", want, html)
		}
	}
	// A body with no codegraph block yields no table (graceful absence).
	if _, ok := codegraphPackageTable("# just a heading, no block"); ok {
		t.Errorf("expected no table when the body carries no codegraph block")
	}
	// A block that does not parse as JSON yields no table.
	if _, ok := codegraphPackageTable("```codegraph\nnot json\n```"); ok {
		t.Errorf("expected no table when the codegraph block is not valid JSON")
	}
}

// TestProjectDetailCodegraphTableRenders asserts the derived package table is rendered in
// the codegraph card when CodegraphTable is set, and absent otherwise — the no-JS-readable
// peer to the client-side diagram (story 3).
func TestProjectDetailCodegraphTableRenders(t *testing.T) {
	withTable := renderProjectDetail(t, projectDetailData{
		Project:        projectRow{ID: "proj_x", Name: "x"},
		CodegraphHTML:  renderMarkdown("```codegraph\n{\"module\":\"m\",\"nodes\":[],\"edges\":[]}\n```"),
		CodegraphTable: template.HTML(`<table class="codegraph-packages"><tbody></tbody></table>`),
	})
	if !strings.Contains(withTable, `data-field="codegraph-packages-wrap"`) {
		t.Errorf("codegraph card missing the derived package-table wrap when CodegraphTable is set")
	}
	if !strings.Contains(withTable, `class="codegraph-packages"`) {
		t.Errorf("codegraph card did not render the derived package table")
	}
}

// TestRenderMarkdownCodegraphFence verifies a ```codegraph fence survives the safe
// goldmark renderer as a class="language-codegraph" code block — the hook
// codegraph-init.js keys off to render the interactive graph (sty_a0da6bf9).
func TestRenderMarkdownCodegraphFence(t *testing.T) {
	html := string(renderMarkdown("```codegraph\n{\"module\":\"m\",\"nodes\":[],\"edges\":[]}\n```"))
	if !strings.Contains(html, `class="language-codegraph"`) {
		t.Fatalf("rendered codegraph fence missing language-codegraph class:\n%s", html)
	}
}

// TestCodegraphAssetsEmbedded verifies the vendored cytoscape bundle + the lazy-load
// init are embedded and served, and that the project page wires the init in.
func TestCodegraphAssetsEmbedded(t *testing.T) {
	for _, asset := range []string{"static/cytoscape.min.js", "static/codegraph-init.js"} {
		b, err := assets.ReadFile(asset)
		if err != nil || len(b) == 0 {
			t.Fatalf("embedded asset %s missing or empty: %v", asset, err)
		}
	}
	b, err := assets.ReadFile("templates/project_detail.html")
	if err != nil {
		t.Fatalf("read project_detail.html: %v", err)
	}
	if !strings.Contains(string(b), "/static/codegraph-init.js") {
		t.Errorf("project_detail.html does not include codegraph-init.js")
	}
}

// TestCodegraphInitLazyGuard asserts the init script only loads the cytoscape bundle
// when a language-codegraph block is present — the same lazy guard as mermaid-init
// (AC3: a page with no block never fetches the bundle).
func TestCodegraphInitLazyGuard(t *testing.T) {
	b, err := assets.ReadFile("static/codegraph-init.js")
	if err != nil {
		t.Fatalf("read codegraph-init.js: %v", err)
	}
	src := string(b)
	if !strings.Contains(src, "pre > code.language-codegraph") {
		t.Errorf("codegraph-init.js does not detect the language-codegraph block")
	}
	// The bundle <script src> must be reachable only after the block check returns —
	// i.e. the file guards the load behind blocks.length. We assert both markers exist;
	// the chromedp render test proves the runtime behaviour.
	if !strings.Contains(src, "/static/cytoscape.min.js") {
		t.Errorf("codegraph-init.js never references the cytoscape bundle")
	}
	if !strings.Contains(src, "if (!blocks.length)") {
		t.Errorf("codegraph-init.js missing the no-block early return (lazy guard)")
	}
}
