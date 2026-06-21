package taskepisode

import (
	"testing"
	"time"
)

// TestProject pins the read-time execution-episode projection (epic:project-tasks
// re-runnable tasks): a status_transition into `running` opens an episode; the
// next transition into a terminal state closes it; every row in between groups
// into the open episode. Two runs of the same task must surface as two distinct
// episodes with their own start/end timestamps, and re-running must not disturb
// the earlier episode.
func TestProject(t *testing.T) {
	at := func(min int) time.Time { return time.Date(2026, 6, 20, 12, min, 0, 0, time.UTC) }
	rows := []Row{
		{Kind: "task_created", Created: at(0)},
		{Kind: "review_requested", Created: at(1)},
		// run 1
		{Kind: "status_transition", To: "running", Created: at(2)},
		{Kind: "task_updated", Created: at(3)}, // the work
		{Kind: "review_accept", Created: at(4)},
		{Kind: "status_transition", To: "complete", Created: at(5)},
		// run 2 (re-run)
		{Kind: "status_transition", To: "running", Created: at(10)},
		{Kind: "task_updated", Created: at(11)},
		{Kind: "status_transition", To: "complete", Created: at(12)},
		// run 3 still open
		{Kind: "status_transition", To: "running", Created: at(20)},
		{Kind: "task_updated", Created: at(21)},
	}

	eps := Project(rows)
	if len(eps) != 3 {
		t.Fatalf("expected 3 episodes, got %d", len(eps))
	}

	// run 1: closed, complete, start at(2) end at(5); rows = running + 3 = 4
	if eps[0].Run != 1 || !eps[0].Start.Equal(at(2)) || !eps[0].End.Equal(at(5)) || eps[0].EndTo != "complete" {
		t.Errorf("run 1 wrong: %+v", eps[0])
	}
	if eps[0].Rows != 4 {
		t.Errorf("run 1 rows = %d, want 4", eps[0].Rows)
	}
	// StartIdx lets a caller slice source[StartIdx:StartIdx+Rows] per episode.
	if eps[0].StartIdx != 2 || eps[1].StartIdx != 6 || eps[2].StartIdx != 9 {
		t.Errorf("episode StartIdx wrong: run1=%d (want 2), run2=%d (want 6), run3=%d (want 9)",
			eps[0].StartIdx, eps[1].StartIdx, eps[2].StartIdx)
	}

	// run 2: closed, complete, start at(10) end at(12)
	if eps[1].Run != 2 || !eps[1].Start.Equal(at(10)) || !eps[1].End.Equal(at(12)) || eps[1].EndTo != "complete" {
		t.Errorf("run 2 wrong: %+v", eps[1])
	}

	// run 3: still open — no end, no endTo
	if eps[2].Run != 3 || !eps[2].Open() || eps[2].EndTo != "" {
		t.Errorf("run 3 should be open: %+v", eps[2])
	}
}
