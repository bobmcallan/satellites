package server

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/verb"
)

// TestTaskRowsFromDocsDropsLibraryPublications pins sty_ef0ccc89: the tasks
// panel lists only a project's runnable tasks. A library-scoped publication
// carries the publisher's project_id, so document_list by project_id sweeps it
// in — but it is a distribution artifact, not a task to run here, and must be
// dropped. The project-scoped task with the same project_id stays.
func TestTaskRowsFromDocsDropsLibraryPublications(t *testing.T) {
	resp := verb.DocumentListResponse{Items: []document.Document{
		{ID: "tsk_90b45e3a", Name: "Codegraph", Type: "task", Scope: document.ScopeProject, ProjectID: "proj_fc7d72d8", Category: "task", Status: "complete"},
		{ID: "doc_d674f51f", Name: "Codegraph", Type: "task", Scope: document.ScopeLibrary, ProjectID: "proj_fc7d72d8", Status: "active"},
	}}
	rows := taskRowsFromDocs(resp)
	if len(rows) != 1 {
		t.Fatalf("want 1 row (library publication dropped), got %d: %+v", len(rows), rows)
	}
	if rows[0].ID != "tsk_90b45e3a" {
		t.Errorf("kept the wrong row: want tsk_90b45e3a (project-scoped), got %q", rows[0].ID)
	}
	for _, r := range rows {
		if r.ID == "doc_d674f51f" {
			t.Error("library-scoped publication leaked into the tasks panel")
		}
	}
}

// TestProjectDetailTasksPanelRenders pins the inline-expand tasks panel
// (sty_f46fe4f2 AC#1/#2/#4 + sty_f2f6465d chips): the row headline + status
// pill + category/tag chips, the expandable detail row with the task content,
// and the ledger/log grouped by execution episode (run-N → its rows). It is a
// pure view-model render (no verbs), so it runs anywhere.
func TestProjectDetailTasksPanelRenders(t *testing.T) {
	data := projectDetailData{
		Title:        "Demo · projects · satellites",
		Project:      projectRow{ID: "proj_demo", Name: "Demo"},
		TaskFiltered: 1,
		TaskTotal:    3,
		Tasks: []taskRow{
			{
				ID: "tsk_demo", Title: "Secrets / code scan", Status: "active",
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
		// sty_09e2ce68: grid parity with the story table — lowercase col-* headers,
		// a dedicated col-id copy cell, and a <code> status pill.
		`<th class="col-id">id</th>`,
		`<th class="col-title">title</th>`,
		`<th class="col-status">status</th>`,
		`<th class="col-runs">runs</th>`,
		`<th class="col-lastrun">last run</th>`,
		`@click.stop="copyTaskID('tsk_demo', $event)"`,
		`data-field="task-id-copy"`,
		// sty_c54160e1: standing status vocabulary active/running/inactive.
		`<code class="status-pill" data-field="task-status">active</code>`,
		`colspan="5"`,                  // detail row spans all 5 columns
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
		// sty_80447ada search + Filtered/Total count
		`data-field="panel-tasks-search"`,
		`@keydown.enter.prevent="applyToServer()"`,
		`data-field="tasks-count-indicator"`,
		"1 / 3", // filtered / total
		`data-task-filtered="1"`,
		`data-task-total="3"`,
		// sty_c54160e1: stories-style filter chip strip below the search (in the
		// same position) replaces the lone header toggle.
		`data-field="panel-tasks-chips"`,
		`chip in getEffectiveChips()`,
		`@click="removeChip(chip.key, chip.value)"`,
		`data-action="panel-tasks-clear-all"`,
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
	// sty_c54160e1: the lone header status toggle is gone (replaced by the chip strip).
	if strings.Contains(out, "task-status-toggle") || strings.Contains(out, "toggleStatusAll") {
		t.Error("project_detail tasks panel still has the removed status toggle")
	}
	// The count indicator lives in the tasks panel-header (top-right, like
	// stories): it appears AFTER the <h2>tasks</h2> and BEFORE the search box.
	hdr := strings.Index(out, `data-field="tasks-count-indicator"`)
	search := strings.Index(out, `data-field="panel-tasks-search"`)
	if hdr < 0 || search < 0 || hdr > search {
		t.Error("tasks count indicator is not in the panel-header above the search box")
	}
}
