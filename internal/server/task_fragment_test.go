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

// TestTaskRowsFragmentEmpty: the block renders nothing (no row markup) for an
// empty task set — so a live refetch of an empty/filtered panel clears cleanly.
func TestTaskRowsFragmentEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := projectDetailTmpl.ExecuteTemplate(&buf, "task-rows", projectDetailData{}); err != nil {
		t.Fatalf("render empty task-rows: %v", err)
	}
	if strings.Contains(buf.String(), `data-field="task-row"`) {
		t.Errorf("empty task-rows should render no task rows, got:\n%s", buf.String())
	}
}
