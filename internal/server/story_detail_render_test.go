package server

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
	"time"
)

// renderStoryDocsFragment executes the story-docs fragment template (the lazy-
// load target for the inline panel's Documents tab) and returns the HTML.
func renderStoryDocsFragment(t *testing.T, data storyDocsData) string {
	t.Helper()
	var buf bytes.Buffer
	if err := storyDetailTmpl.ExecuteTemplate(&buf, "story-docs", data); err != nil {
		t.Fatalf("execute story-docs template: %v", err)
	}
	return buf.String()
}

// TestStoryDocsFragmentLists pins sty_bf2fc8e1 AC2: the attached-documents
// fragment lists each document's name/id/updated with its rendered markdown body
// in a native <details> (expand/collapse without Alpine).
func TestStoryDocsFragmentLists(t *testing.T) {
	html := renderStoryDocsFragment(t, storyDocsData{
		Documents: []docRow{{
			ID:        "doc_abc",
			Name:      "implementation-summary",
			TypeTag:   "summary",
			UpdatedAt: time.Date(2026, 6, 25, 2, 2, 0, 0, time.UTC),
			BodyHTML:  template.HTML(`<p>what was implemented</p>`),
		}},
	})

	if !strings.Contains(html, "<details") {
		t.Errorf("AC2: documents not rendered as expandable <details>:\n%s", html)
	}
	if !strings.Contains(html, `data-field="story-document-row"`) || !strings.Contains(html, "implementation-summary") {
		t.Errorf("AC2: attached document name missing")
	}
	if !strings.Contains(html, "doc_abc") || !strings.Contains(html, "2026-06-25 02:02") {
		t.Errorf("AC2: attached document id/updated not listed")
	}
	if !strings.Contains(html, "what was implemented") {
		t.Errorf("AC2: attached document body did not render")
	}
}

// TestStoryDocsFragmentEmpty pins sty_bf2fc8e1 AC3: the empty state shows when a
// story has no attached documents.
func TestStoryDocsFragmentEmpty(t *testing.T) {
	html := renderStoryDocsFragment(t, storyDocsData{})
	if !strings.Contains(html, `data-field="story-documents-empty"`) {
		t.Errorf("AC3: empty-documents state not rendered:\n%s", html)
	}
}
