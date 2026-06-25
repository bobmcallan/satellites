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

// TestStoryDocsFragmentLists pins the attached-documents fragment columns
// (sty_aacf9c95): each expandable document is a project-panel-style table row —
// id | name (+ phase/tag chips) | updated — with its rendered markdown body in a
// sibling detail row (expand/collapse without Alpine).
func TestStoryDocsFragmentLists(t *testing.T) {
	html := renderStoryDocsFragment(t, storyDocsData{
		Documents: []docRow{{
			ID:         "doc_abc",
			Name:       "implementation-summary",
			TypeTag:    "summary",
			PhaseTag:   "build",
			OtherTags:  []string{"area:portal"},
			UpdatedAt:  time.Date(2026, 6, 25, 2, 2, 0, 0, time.UTC),
			Expandable: true,
			BodyHTML:   template.HTML(`<p>what was implemented</p>`),
		}},
	})

	for _, want := range []string{
		`data-field="story-documents-table"`,          // project-panel-style table
		`<th class="col-id">id</th>`,                  // id column header
		`<th class="col-title">name</th>`,             // name column header
		`<th class="col-updated">updated</th>`,        // updated column header
		`data-field="story-document-row"`,             // a document row
		`data-expandable="true"`,                      // expandable (has a body)
		"implementation-summary",                      // name
		"doc_abc",                                     // id
		"2026-06-25 02:02",                            // updated
		`<span class="phase-chip">phase:build</span>`, // phase chip (display-only)
		`<span class="tag-chip">area:portal</span>`,   // tag chip (display-only)
		`data-detail-for="doc_abc"`,                   // the detail row
		"what was implemented",                        // body rendered
	} {
		if !strings.Contains(html, want) {
			t.Errorf("AC2: docs-tab columns missing %q\n%s", want, html)
		}
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

// TestStoryDocsFragmentNoSearch pins sty_aacf9c95 AC1: the story Documents tab
// has NO search/filter box and no chip-as-filter behaviour — chips render as
// plain (non-clickable) labels, not data-chip filter buttons.
func TestStoryDocsFragmentNoSearch(t *testing.T) {
	html := renderStoryDocsFragment(t, storyDocsData{
		Documents: []docRow{{
			ID: "doc_abc", Name: "summary", TypeTag: "summary",
			PhaseTag: "build", OtherTags: []string{"area:portal"},
			UpdatedAt: time.Date(2026, 6, 25, 2, 2, 0, 0, time.UTC),
		}},
	})
	for _, banned := range []string{
		`data-field="story-documents-search"`, // no search input
		`data-field="story-documents-count"`,  // no count badge
		`data-chip=`,                          // no chip-as-filter buttons
		`panel-search`,                        // no search box styling
	} {
		if strings.Contains(html, banned) {
			t.Errorf("AC1: story docs tab must not contain %q\n%s", banned, html)
		}
	}
}
