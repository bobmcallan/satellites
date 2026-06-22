package server

import (
	"bytes"
	"strings"
	"testing"
)

// TestTaskRowsFragmentRenders pins the live-refresh target (sty_4dc86c77): the
// `task-rows` template block — rendered standalone by taskFragmentHandler and
// swapped into the panel tbody on the project:<id> SSE trigger — produces the
// task row with its live columns (STATUS / RUNS / LAST RUN). If the block or its
// data shape drifts, the live refetch would swap in broken markup.
func TestTaskRowsFragmentRenders(t *testing.T) {
	var buf bytes.Buffer
	data := projectDetailData{
		Tasks: []taskRow{{
			ID: "tsk_abc123", Title: "stale-epic scan", Status: "active",
			RunCount: 3, LastRun: "2026-06-22 10:00",
		}},
		TaskFiltered: 1, TaskTotal: 1,
	}
	if err := projectDetailTmpl.ExecuteTemplate(&buf, "task-rows", data); err != nil {
		t.Fatalf("render task-rows: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"tsk_abc123",
		`data-field="task-row"`,
		`data-field="task-status"`,
		`data-field="task-runcount">3<`, // RUNS column reflects the run count
		`data-field="task-lastrun"`,
		"2026-06-22 10:00", // LAST RUN column
	} {
		if !strings.Contains(out, want) {
			t.Errorf("task-rows fragment missing %q\n--- got ---\n%s", want, out)
		}
	}
}

// TestTaskRowsFragmentEmpty: for an empty task set the block renders the
// EMPTY-STATE ROW (no task rows) — not nothing. The empty-state living inside the
// fragment is what fixes the 0→1 live-render (sty_33342052): the panel always
// renders the tbody, so liveRefresh swaps this fragment in and the first task
// appears without a full-page refresh. A "none yet" project (TaskTotal == 0) reads
// differently from a filtered-empty one.
func TestTaskRowsFragmentEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := projectDetailTmpl.ExecuteTemplate(&buf, "task-rows", projectDetailData{}); err != nil {
		t.Fatalf("render empty task-rows: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, `data-field="task-row"`) {
		t.Errorf("empty task-rows should render no task rows, got:\n%s", out)
	}
	for _, want := range []string{`data-field="no-tasks"`, "No tasks in this project yet."} {
		if !strings.Contains(out, want) {
			t.Errorf("empty task-rows must carry the empty-state row %q (the 0→1 live-render target)\n--- got ---\n%s", want, out)
		}
	}
}

// TestTaskRowsFragmentEmptyFiltered: an empty result with a non-zero project total
// is a filtered-empty view, not a never-populated one — the empty-state row says so.
func TestTaskRowsFragmentEmptyFiltered(t *testing.T) {
	var buf bytes.Buffer
	if err := projectDetailTmpl.ExecuteTemplate(&buf, "task-rows", projectDetailData{TaskTotal: 3}); err != nil {
		t.Fatalf("render filtered-empty task-rows: %v", err)
	}
	if !strings.Contains(buf.String(), "No tasks match the active filter.") {
		t.Errorf("filtered-empty task-rows must read 'No tasks match the active filter.'\n--- got ---\n%s", buf.String())
	}
}

// TestStoryRowsFragmentEmpty mirrors the task case: the story-rows fragment carries
// the empty-state row so the story panel's 0→1 live-render works too (shared fix).
func TestStoryRowsFragmentEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := projectDetailTmpl.ExecuteTemplate(&buf, "story-rows", projectDetailData{}); err != nil {
		t.Fatalf("render empty story-rows: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, `class="story-row"`) {
		t.Errorf("empty story-rows should render no story rows, got:\n%s", out)
	}
	if !strings.Contains(out, "No stories under this project yet.") {
		t.Errorf("empty story-rows must carry the empty-state row (the 0→1 live-render target)\n--- got ---\n%s", out)
	}
}
