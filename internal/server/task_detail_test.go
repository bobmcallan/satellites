package server

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
)

// TestProjectDetailTasksPanelRenders pins the inline-expand tasks panel
// (sty_f46fe4f2 AC#1/#2/#4 + sty_f2f6465d chips): the row headline + status
// pill + category/tag chips, the expandable detail row with the task content,
// and the ledger/log grouped by execution episode (run-N → its rows). It is a
// pure view-model render (no verbs), so it runs anywhere.
func TestProjectDetailTasksPanelRenders(t *testing.T) {
	data := projectDetailData{
		Title:   "Demo · projects · satellites",
		Project: projectRow{ID: "proj_demo", Name: "Demo"},
		Tasks: []taskRow{
			{
				ID: "tsk_demo", Title: "Secrets / code scan", Status: "complete",
				Category: "task", Tags: []string{"skill:secrets-scan", "workflow:satellites-task-workflow"},
				RunCount: 2, LastRun: "2026-06-21 01:05",
				BodyHTML: template.HTML("<p>Scan tracked files for secrets.</p>"),
				Episodes: []taskEpisodeView{
					{
						Run: 1, Start: "2026-06-20 12:00:00Z", End: "2026-06-20 12:02:00Z",
						Status: "complete", Open: false,
						Rows: []taskLedgerRowView{
							{When: "2026-06-20 12:00:00Z", Kind: "status_transition", Transition: "→ running", Actor: "usr_x", Body: "ready → running"},
							{When: "2026-06-20 12:02:00Z", Kind: "status_transition", Transition: "→ complete", Actor: "usr_x", Body: "running → complete"},
						},
					},
					{Run: 2, Start: "2026-06-20 13:00:00Z", Status: "running (open)", Open: true},
				},
			},
		},
	}
	var buf bytes.Buffer
	if err := projectDetailTmpl.Execute(&buf, data); err != nil {
		t.Fatalf("project_detail template render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"project-tasks",               // the panel
		`x-data="taskPanel"`,          // inline-expand component
		`data-field="task-row-title"`, // headline
		"Secrets / code scan",         // task title
		`<span class="status-pill" data-field="task-status">complete</span>`, // status pill
		"2026-06-21 01:05",             // last-run
		`@click="toggleTaskRow"`,       // row click expands inline
		`data-detail-for="tsk_demo"`,   // the inline detail row
		`x-show="isTaskExpanded($el)"`, // gated on the row being expanded
		// content rendered inline as safe markdown
		`data-field="task-content-body"`,
		"Scan tracked files for secrets.",
		// ledger/log grouped by execution episode
		"executions (2)",
		`data-field="episode-run">run-1`,
		`data-field="episode-run">run-2`,
		"→ complete",     // a transition row
		"running (open)", // the open episode status
		// sty_f2f6465d chips (carried through the inline-expand rework)
		`class="category-chip is-clickable" data-category="task"`,
		`class="tag-chip is-clickable" data-tag="skill:secrets-scan"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("project_detail tasks panel missing %q", want)
		}
	}
	// The two tags must render as SEPARATE chips, not a concatenated string.
	if strings.Contains(out, "skill:secrets-scanworkflow:satellites-task-workflow") {
		t.Error("project_detail tasks panel rendered tags as a run-together string")
	}
	// Read-only invariant: the inline task view never offers a status-write path.
	if strings.Contains(out, "toggleTaskStatus") || strings.Contains(out, `data-action="task-status-set"`) {
		t.Error("project_detail tasks panel exposed a task status write path")
	}
}
