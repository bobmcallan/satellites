package portal

import (
	"html"
	"html/template"
	"regexp"
	"strings"
)

// RenderHelpMarkdown converts a constrained subset of Markdown to
// HTML suitable for display on a help page. Supported:
//   - ATX headings: # / ## / ###
//   - paragraphs
//   - fenced code blocks (```)
//   - unordered lists (- prefix)
//   - inline code (`...`)
//   - emphasis (**bold**, *italic*)
//   - links [text](url) (http(s) + relative only)
//   - horizontal rule (---)
//
// Raw HTML in the input is escaped, so embedded `<script>` becomes
// `&lt;script&gt;` — keeps help content CSP-safe even if a future
// authoring slip lets through a tag.
//
// story_42f2f2c0.
func RenderHelpMarkdown(src string) template.HTML {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	lines := strings.Split(src, "\n")

	var out strings.Builder
	var (
		inCode   bool
		inList   bool
		paraBuf  []string
		listItem strings.Builder
	)

	flushPara := func() {
		if len(paraBuf) == 0 {
			return
		}
		out.WriteString("<p>")
		out.WriteString(renderInline(strings.Join(paraBuf, " ")))
		out.WriteString("</p>\n")
		paraBuf = nil
	}
	closeList := func() {
		if !inList {
			return
		}
		out.WriteString("</ul>\n")
		inList = false
	}

	for _, raw := range lines {
		if inCode {
			if strings.HasPrefix(raw, "```") {
				out.WriteString("</code></pre>\n")
				inCode = false
				continue
			}
			out.WriteString(html.EscapeString(raw))
			out.WriteString("\n")
			continue
		}
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(raw, "```") {
			flushPara()
			closeList()
			out.WriteString(`<pre><code>`)
			inCode = true
			continue
		}
		if trimmed == "" {
			flushPara()
			closeList()
			continue
		}
		if trimmed == "---" {
			flushPara()
			closeList()
			out.WriteString("<hr>\n")
			continue
		}
		if strings.HasPrefix(trimmed, "### ") {
			flushPara()
			closeList()
			out.WriteString("<h3>")
			out.WriteString(renderInline(strings.TrimPrefix(trimmed, "### ")))
			out.WriteString("</h3>\n")
			continue
		}
		if strings.HasPrefix(trimmed, "## ") {
			flushPara()
			closeList()
			out.WriteString("<h2>")
			out.WriteString(renderInline(strings.TrimPrefix(trimmed, "## ")))
			out.WriteString("</h2>\n")
			continue
		}
		if strings.HasPrefix(trimmed, "# ") {
			flushPara()
			closeList()
			out.WriteString("<h1>")
			out.WriteString(renderInline(strings.TrimPrefix(trimmed, "# ")))
			out.WriteString("</h1>\n")
			continue
		}
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			flushPara()
			if !inList {
				out.WriteString("<ul>\n")
				inList = true
			}
			listItem.Reset()
			listItem.WriteString("<li>")
			listItem.WriteString(renderInline(trimmed[2:]))
			listItem.WriteString("</li>\n")
			out.WriteString(listItem.String())
			continue
		}
		closeList()
		paraBuf = append(paraBuf, trimmed)
	}
	if inCode {
		out.WriteString("</code></pre>\n")
	}
	flushPara()
	closeList()

	return template.HTML(out.String())
}

var (
	inlineCodePattern = regexp.MustCompile("`([^`]+)`")
	boldPattern       = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	italicPattern     = regexp.MustCompile(`\*([^*]+)\*`)
	linkPattern       = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
)

// renderInline applies inline-level transformations after escaping
// raw HTML. Order matters: code spans first so their contents aren't
// re-processed; then links; then bold; then italic.
func renderInline(s string) string {
	escaped := html.EscapeString(s)

	// Inline code: `x` → <code>x</code>. The escape above means the
	// content is already safe.
	escaped = inlineCodePattern.ReplaceAllString(escaped, "<code>$1</code>")

	// Links: only http(s) and relative paths admitted. Anything else
	// is left as the original text — keeps mailto: / javascript: out.
	escaped = linkPattern.ReplaceAllStringFunc(escaped, func(match string) string {
		groups := linkPattern.FindStringSubmatch(match)
		if len(groups) != 3 {
			return match
		}
		text, href := groups[1], groups[2]
		if !isSafeHelpHref(href) {
			return text
		}
		return `<a href="` + href + `">` + text + `</a>`
	})

	escaped = boldPattern.ReplaceAllString(escaped, "<strong>$1</strong>")
	escaped = italicPattern.ReplaceAllString(escaped, "<em>$1</em>")
	return escaped
}

// isSafeHelpHref guards against javascript: / data: / file: schemes.
// Only http(s) and same-origin relative URLs are admitted.
func isSafeHelpHref(href string) bool {
	switch {
	case strings.HasPrefix(href, "http://"):
		return true
	case strings.HasPrefix(href, "https://"):
		return true
	case strings.HasPrefix(href, "/"):
		return true
	case strings.HasPrefix(href, "#"):
		return true
	}
	// Bare paths (no scheme, no leading slash) are disallowed —
	// avoids ambiguity with custom schemes.
	return false
}
