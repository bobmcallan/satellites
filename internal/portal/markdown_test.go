package portal

import (
	"strings"
	"testing"
)

// TestRenderHelpMarkdown_HeadingsAndParagraphs covers the AC2/AC4
// rendering path: headings and paragraphs produce the expected HTML
// markers.
func TestRenderHelpMarkdown_HeadingsAndParagraphs(t *testing.T) {
	t.Parallel()
	src := "# Title\n\n## Subhead\n\nA paragraph here.\n"
	got := string(RenderHelpMarkdown(src))
	for _, want := range []string{"<h1>Title</h1>", "<h2>Subhead</h2>", "<p>A paragraph here.</p>"} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered HTML missing %q; got:\n%s", want, got)
		}
	}
}

// TestRenderHelpMarkdown_ListAndCode covers fenced code + unordered
// list rendering.
func TestRenderHelpMarkdown_ListAndCode(t *testing.T) {
	t.Parallel()
	src := "- one\n- two\n\n```\ncode line\n```\n"
	got := string(RenderHelpMarkdown(src))
	if !strings.Contains(got, "<ul>") || !strings.Contains(got, "<li>one</li>") {
		t.Errorf("list not rendered: %s", got)
	}
	if !strings.Contains(got, "<pre><code>code line\n</code></pre>") {
		t.Errorf("fenced code not rendered: %s", got)
	}
}

// TestRenderHelpMarkdown_InlineMarks covers bold/italic/code/link.
func TestRenderHelpMarkdown_InlineMarks(t *testing.T) {
	t.Parallel()
	src := "Use **bold**, *italic*, `code`, and [link](https://example.com)."
	got := string(RenderHelpMarkdown(src))
	for _, want := range []string{
		"<strong>bold</strong>",
		"<em>italic</em>",
		"<code>code</code>",
		`<a href="https://example.com">link</a>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in: %s", want, got)
		}
	}
}

// TestRenderHelpMarkdown_EscapesHTML covers AC4: raw HTML in input is
// escaped, so a `<script>` tag in help content cannot execute.
func TestRenderHelpMarkdown_EscapesHTML(t *testing.T) {
	t.Parallel()
	src := "Beware: <script>alert(1)</script>\n"
	got := string(RenderHelpMarkdown(src))
	if strings.Contains(got, "<script>") {
		t.Errorf("rendered HTML still contains raw <script>; got: %s", got)
	}
	if !strings.Contains(got, "&lt;script&gt;") {
		t.Errorf("expected escaped <script>; got: %s", got)
	}
}

// TestRenderHelpMarkdown_RejectsUnsafeLinkScheme covers a CSP-safety
// edge case: a `javascript:` URL is dropped, and the link text is
// preserved as plain text.
func TestRenderHelpMarkdown_RejectsUnsafeLinkScheme(t *testing.T) {
	t.Parallel()
	src := "Click [here](javascript:alert(1)) please."
	got := string(RenderHelpMarkdown(src))
	if strings.Contains(got, "javascript:") {
		t.Errorf("unsafe scheme leaked into output: %s", got)
	}
}
