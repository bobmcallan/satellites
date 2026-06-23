package server

import (
	"strings"
	"testing"
)

// TestRenderMarkdownMermaidFence verifies a ```mermaid fence survives the safe
// goldmark renderer as a class="language-mermaid" code block — the hook
// mermaid-init.js keys off to render the diagram (sty_89bd084f).
func TestRenderMarkdownMermaidFence(t *testing.T) {
	html := string(renderMarkdown("```mermaid\ngraph LR\n  a --> b\n```"))
	if !strings.Contains(html, `class="language-mermaid"`) {
		t.Fatalf("rendered mermaid fence missing language-mermaid class:\n%s", html)
	}
}

// TestMermaidAssetsEmbedded verifies the vendored bundle + the lazy-load init are
// embedded and served, and that every doc-body template wires the init in.
func TestMermaidAssetsEmbedded(t *testing.T) {
	for _, asset := range []string{"static/mermaid.min.js", "static/mermaid-init.js"} {
		b, err := assets.ReadFile(asset)
		if err != nil || len(b) == 0 {
			t.Fatalf("embedded asset %s missing or empty: %v", asset, err)
		}
	}
	for _, tmpl := range []string{
		"templates/project_detail.html",
		"templates/workspace_detail.html",
		"templates/story_detail.html",
	} {
		b, err := assets.ReadFile(tmpl)
		if err != nil {
			t.Fatalf("read %s: %v", tmpl, err)
		}
		if !strings.Contains(string(b), "/static/mermaid-init.js") {
			t.Errorf("%s does not include mermaid-init.js", tmpl)
		}
	}
}
