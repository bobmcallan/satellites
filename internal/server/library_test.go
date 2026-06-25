package server

import (
	"bytes"
	"strings"
	"testing"
)

// renderLibrary executes the real library template deterministically (no store,
// no session) and returns the HTML — the project's server-render test pattern.
func renderLibrary(t *testing.T, data libraryData) string {
	t.Helper()
	var buf bytes.Buffer
	if err := libraryTmpl.Execute(&buf, data); err != nil {
		t.Fatalf("execute library template: %v", err)
	}
	return buf.String()
}

// TestLibraryPageRendersTasksOnly pins the tasks-only library surface
// (sty_98956dbb) plus library-parity (sty_def7ecca): the page lists published
// tasks with a leading ID column, the publisher as a tag chip (no publisher
// column), a server-side search box, and no kind chips / skill rows.
func TestLibraryPageRendersTasksOnly(t *testing.T) {
	html := renderLibrary(t, libraryData{
		Title:     "task library",
		ActiveNav: "library",
		Query:     "",
		Tasks: []libraryTaskRow{
			{ID: "doc_d674f51f", Name: "Codegraph", Description: "Re-runnable codegraph job",
				Tags: []string{"publisher:proj_fc7d72d8", "workflow:satellites-task-workflow"}},
		},
	})

	// AC1: a leading ID column shows the publication id.
	if !strings.Contains(html, "<th>ID</th>") || !strings.Contains(html, `data-task-id="doc_d674f51f"`) || !strings.Contains(html, "doc_d674f51f") {
		t.Errorf("AC1: id column / id value missing:\n%s", html)
	}
	// AC2: publisher is a tag chip, not a column.
	if strings.Contains(html, "PUBLISHER") || strings.Contains(html, "task-publisher") {
		t.Errorf("AC2: a PUBLISHER column is still rendered")
	}
	if !strings.Contains(html, `data-tag="publisher:proj_fc7d72d8"`) {
		t.Errorf("AC2: publisher not rendered as a tag chip:\n%s", html)
	}
	// AC3: the server-side search box is present.
	if !strings.Contains(html, `data-field="library-search-input"`) || !strings.Contains(html, `name="library_q"`) {
		t.Errorf("AC3: server-side search box missing")
	}
	// Still tasks-only: no kind chips, no skill markup.
	if strings.Contains(html, "data-kind-filter") || strings.Contains(html, "data-skill-name") {
		t.Errorf("tasks-only invariant broken (kind chips / skill rows present)")
	}
}

// TestLibrarySearchRehydrates pins AC3: the active library_q rehydrates the
// search box, and the empty state names the query that matched nothing.
func TestLibrarySearchRehydrates(t *testing.T) {
	html := renderLibrary(t, libraryData{ActiveNav: "library", Query: "tags:publisher:proj_x"})
	if !strings.Contains(html, `value="tags:publisher:proj_x"`) {
		t.Errorf("AC3: search box did not rehydrate the active query:\n%s", html)
	}
	if !strings.Contains(html, `data-field="library-empty"`) || !strings.Contains(html, "tags:publisher:proj_x") {
		t.Errorf("AC3: query-aware empty state missing")
	}
}

// TestFilterLibraryTasks pins AC3: server-side filtering matches free text
// against the DESCRIPTION (and name/id/tags) and `tags:<v>` tokens against the
// row's tags (including the synthesised publisher: tag).
func TestFilterLibraryTasks(t *testing.T) {
	rows := []libraryTaskRow{
		{ID: "doc_a", Name: "Codegraph", Description: "Render the package dependency graph",
			Tags: []string{"publisher:proj_fc7d72d8", "area:codegraph"}},
		{ID: "doc_b", Name: "corpus-summarise", Description: "Summarise the workspace corpus",
			Tags: []string{"publisher:proj_682cfeed"}},
	}

	// Free text hits the description of exactly one row.
	got := filterLibraryTasks(rows, parseStoryQuery("dependency"))
	if len(got) != 1 || got[0].ID != "doc_a" {
		t.Errorf("free-text description match: got %+v", got)
	}
	// A publisher tag token filters to that publisher's tasks.
	got = filterLibraryTasks(rows, parseStoryQuery("tags:publisher:proj_682cfeed"))
	if len(got) != 1 || got[0].ID != "doc_b" {
		t.Errorf("publisher tag filter: got %+v", got)
	}
	// An empty query returns everything (library tasks have no terminal status).
	if got = filterLibraryTasks(rows, parseStoryQuery("")); len(got) != 2 {
		t.Errorf("empty query should list all: got %d", len(got))
	}
}

// TestNavbarHasNoLedgerLink pins sty_4020cff6: the navbar no longer carries the
// Ledger link (the route/handler stay reachable by direct URL), while the other
// primary-nav items remain. The library page is a representative carrier of the
// shared nav block.
func TestNavbarHasNoLedgerLink(t *testing.T) {
	html := renderLibrary(t, libraryData{ActiveNav: "library"})
	if strings.Contains(html, `data-nav="ledger"`) || strings.Contains(html, ">LEDGER<") {
		t.Errorf("navbar still renders a Ledger link:\n%s", html)
	}
	if !strings.Contains(html, `data-nav="library"`) {
		t.Errorf("expected the LIBRARY nav item to remain")
	}
}
