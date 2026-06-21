package server

import (
	"bytes"
	"strings"
	"testing"
)

// TestTaskDetailTemplateRenders pins epic:workflow-steps order-6 AC#4: the task
// detail template executes without error and surfaces the task body + each
// execution episode with its log rows. A pure view-model render (no verbs), so
// it runs anywhere.
func TestTaskDetailTemplateRenders(t *testing.T) {
	data := taskDetailData{
		Title:     "Secrets scan · tasks · satellites",
		TaskID:    "tsk_demo",
		TaskTitle: "Secrets / code scan",
		Status:    "complete",
		ProjectID: "proj_demo",
		Body:      "<p>Scan tracked files for secrets.</p>",
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
	}

	var buf bytes.Buffer
	if err := taskDetailTmpl.Execute(&buf, data); err != nil {
		t.Fatalf("task_detail template render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"Secrets / code scan", // title
		"tsk_demo",            // id
		"Executions (2)",      // tab count
		"run-1", "run-2",      // both episodes
		"→ complete",          // a transition row
		"running (open)",      // the open episode status
		"/projects/proj_demo", // back link
	} {
		if !strings.Contains(out, want) {
			t.Errorf("task_detail render missing %q", want)
		}
	}
}

// TestProjectDetailTasksPanelRenders pins AC#1: the project-detail tasks panel
// lists tasks (title, status, run-count, last-run) peer to the stories list.
func TestProjectDetailTasksPanelRenders(t *testing.T) {
	data := projectDetailData{
		Title:   "Demo · projects · satellites",
		Project: projectRow{ID: "proj_demo", Name: "Demo"},
		Tasks: []taskRow{
			{ID: "tsk_demo", Title: "Secrets / code scan", Status: "complete", RunCount: 5, LastRun: "2026-06-21 01:05"},
		},
	}
	var buf bytes.Buffer
	if err := projectDetailTmpl.Execute(&buf, data); err != nil {
		t.Fatalf("project_detail template render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"project-tasks",       // the panel
		"Secrets / code scan", // task title
		"/tasks/tsk_demo",     // deep link to detail
		"2026-06-21 01:05",    // last-run
	} {
		if !strings.Contains(out, want) {
			t.Errorf("project_detail tasks panel missing %q", want)
		}
	}
}
