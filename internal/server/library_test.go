package server

import (
	"bytes"
	"strings"
	"testing"
)

// TestLibraryRowKind pins the task-vs-skill kind resolution (sty_35d8e166): a
// task forces "task"; a skill keeps its kind: tag / frontmatter kind, and falls
// back to "skill" when it declares none — so the filter chips always split the
// two even when a skill carries no explicit kind.
func TestLibraryRowKind(t *testing.T) {
	cases := []struct {
		name     string
		force    string
		tags     []string
		fmKind   string
		defaultK string
		want     string
	}{
		{"task forces task", "task", []string{"epic:x", "kind:ignored"}, "ignored", "task", "task"},
		{"skill kind tag", "", []string{"kind:reviewer"}, "", "skill", "reviewer"},
		{"skill frontmatter kind overrides tag", "", []string{"kind:reviewer"}, "workflow", "skill", "workflow"},
		{"skill no kind defaults to skill", "", []string{"epic:x"}, "", "skill", "skill"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := libraryRowKind(c.force, c.tags, c.fmKind, c.defaultK); got != c.want {
				t.Errorf("libraryRowKind(%q,%v,%q,%q) = %q, want %q", c.force, c.tags, c.fmKind, c.defaultK, got, c.want)
			}
		})
	}
}

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

// TestLibraryPageRendersTasksAndSkills is the AC1/AC2 render test: the library
// page lists tasks alongside skills in one table, shows each row's kind, and
// renders the kind chips (the task-vs-skill filter).
func TestLibraryPageRendersTasksAndSkills(t *testing.T) {
	html := renderLibrary(t, libraryData{
		Title:     "library",
		ActiveNav: "library",
		Skills: []librarySkillRow{
			{Name: "Codegraph", Kind: "task", Description: "Re-runnable codegraph job", Publisher: "proj_pub"},
			{Name: "secrets-scan", Kind: "skill", Description: "Sweep for secrets", Publisher: "proj_pub"},
		},
		Kinds: []libraryKindChip{
			{Kind: "skill", Active: false},
			{Kind: "task", Active: false},
		},
	})

	// AC1: both the task and the skill appear in the table.
	if !strings.Contains(html, `data-skill-name="Codegraph"`) || !strings.Contains(html, `data-skill-kind="task"`) {
		t.Errorf("AC1: published task row missing from library table:\n%s", html)
	}
	if !strings.Contains(html, `data-skill-name="secrets-scan"`) {
		t.Errorf("AC1: skill row missing from library table")
	}
	// AC2: the filter chips include both task and skill.
	if !strings.Contains(html, `data-kind-filter="task"`) || !strings.Contains(html, `data-kind-filter="skill"`) {
		t.Errorf("AC2: task/skill filter chips not rendered:\n%s", html)
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

// TestLibraryPageFilteredEmpty covers the filtered-empty copy (AC3: clears cleanly).
func TestLibraryPageFilteredEmpty(t *testing.T) {
	html := renderLibrary(t, libraryData{ActiveNav: "library", ActiveKind: "task"})
	if !strings.Contains(html, `data-field="library-empty"`) {
		t.Errorf("empty state not rendered for a filter with no matches")
	}
	if !strings.Contains(html, "task") {
		t.Errorf("filtered-empty copy should name the active kind")
	}
}
