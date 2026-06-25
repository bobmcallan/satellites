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
// (sty_98956dbb): the page lists published library-scope tasks (name +
// description + publisher) and carries NO kind-filter chips and NO skill/gate/
// workflow rows — those primitives stay project/user-local and never list here.
func TestLibraryPageRendersTasksOnly(t *testing.T) {
	html := renderLibrary(t, libraryData{
		Title:     "task library",
		ActiveNav: "library",
		Tasks: []libraryTaskRow{
			{Name: "Codegraph", Description: "Re-runnable codegraph job", Publisher: "proj_pub"},
		},
	})

	// AC1: the published task row appears with its description + publisher.
	if !strings.Contains(html, `data-task-name="Codegraph"`) {
		t.Errorf("AC1: published task row missing from library table:\n%s", html)
	}
	if !strings.Contains(html, "Re-runnable codegraph job") || !strings.Contains(html, "proj_pub") {
		t.Errorf("AC1: task description/publisher missing from row")
	}
	// AC2: no kind-filter chips of any kind (capability/gate/task/workflow).
	if strings.Contains(html, "data-kind-filter") || strings.Contains(html, `data-field="library-filter"`) {
		t.Errorf("AC2: kind-filter chips still rendered:\n%s", html)
	}
	// AC1/AC3: no skill surface — the table has no KIND column and no skill rows.
	if strings.Contains(html, "data-skill-name") || strings.Contains(html, "data-skill-kind") {
		t.Errorf("AC1: skill-row markup still present")
	}
	// AC3: tasks-only copy — the lede no longer offers 'skills'.
	if strings.Contains(html, "skills") {
		t.Errorf("AC3: page copy still mentions skills:\n%s", html)
	}
}

// TestLibraryPageEmpty covers the tasks-only empty state copy.
func TestLibraryPageEmpty(t *testing.T) {
	html := renderLibrary(t, libraryData{ActiveNav: "library"})
	if !strings.Contains(html, `data-field="library-empty"`) {
		t.Errorf("empty state not rendered when there are no tasks")
	}
	if !strings.Contains(html, "task publish") {
		t.Errorf("empty-state copy should point at `task publish`")
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
