package server

import (
	"strings"
	"testing"
)

// TestUnescapeLiteralNewlines covers sty_0633bcf5: bodies stored with literal
// backslash-escape sequences are repaired, clean bodies are untouched.
func TestUnescapeLiteralNewlines(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"escaped newlines", `# H1\n\nbody\n- item`, "# H1\n\nbody\n- item"},
		{"escaped crlf", `a\r\nb`, "a\nb"},
		{"escaped tab", `a\tb`, "a\tb"},
		{"clean body untouched", "# H1\n\nreal newline", "# H1\n\nreal newline"},
		{"no escapes untouched", "plain text", "plain text"},
		{"mixed real and escaped", "real\n## Heading\\n\\nmore", "real\n## Heading\n\nmore"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := unescapeLiteralNewlines(c.in); got != c.want {
				t.Errorf("unescapeLiteralNewlines(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestRenderStoryMarkdown_EscapedBody verifies an escaped body renders real
// markdown structure (headings/lists), not one run-on line.
func TestRenderStoryMarkdown_EscapedBody(t *testing.T) {
	html := string(renderStoryMarkdown(`## Findings\n\n- one\n- two`))
	if !strings.Contains(html, "<h2") {
		t.Errorf("escaped heading not rendered: %q", html)
	}
	if !strings.Contains(html, "<li>") {
		t.Errorf("escaped list not rendered: %q", html)
	}
}
