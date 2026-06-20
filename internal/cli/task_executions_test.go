package cli

import (
	"testing"
	"time"
)

// TestProjectEpisodes pins the read-time execution-episode projection
// (epic:project-tasks, re-runnable tasks): a status_transition into `running`
// opens an episode; the next transition into a terminal state closes it; every
// row in between groups into the open episode. Two runs of the same task must
// surface as two distinct episodes with their own start/end timestamps, and
// re-running must not disturb the earlier episode.
func TestProjectEpisodes(t *testing.T) {
	at := func(min int) time.Time { return time.Date(2026, 6, 20, 12, min, 0, 0, time.UTC) }
	rows := []episodeRow{
		{kind: "task_created", created: at(0)},
		{kind: "review_requested", created: at(1)},
		// run 1
		{kind: "status_transition", to: "running", created: at(2)},
		{kind: "task_updated", created: at(3)}, // the work
		{kind: "review_accept", created: at(4)},
		{kind: "status_transition", to: "complete", created: at(5)},
		// run 2 (re-run)
		{kind: "status_transition", to: "running", created: at(10)},
		{kind: "task_updated", created: at(11)},
		{kind: "status_transition", to: "complete", created: at(12)},
		// run 3 still open
		{kind: "status_transition", to: "running", created: at(20)},
		{kind: "task_updated", created: at(21)},
	}

	eps := projectEpisodes(rows)
	if len(eps) != 3 {
		t.Fatalf("expected 3 episodes, got %d", len(eps))
	}

	// run 1: closed, complete, start at(2) end at(5); rows = running + 3 = 4
	if eps[0].run != 1 || !eps[0].start.Equal(at(2)) || !eps[0].end.Equal(at(5)) || eps[0].endTo != "complete" {
		t.Errorf("run 1 wrong: %+v", eps[0])
	}
	if eps[0].rows != 4 {
		t.Errorf("run 1 rows = %d, want 4", eps[0].rows)
	}

	// run 2: closed, complete, start at(10) end at(12)
	if eps[1].run != 2 || !eps[1].start.Equal(at(10)) || !eps[1].end.Equal(at(12)) || eps[1].endTo != "complete" {
		t.Errorf("run 2 wrong: %+v", eps[1])
	}

	// run 3: still open — no end, no endTo
	if eps[2].run != 3 || !eps[2].end.IsZero() || eps[2].endTo != "" {
		t.Errorf("run 3 should be open: %+v", eps[2])
	}

	// re-running never disturbed run 1's recorded bounds.
	if !eps[0].start.Equal(at(2)) || !eps[0].end.Equal(at(5)) {
		t.Errorf("run 1 bounds changed after later runs: %+v", eps[0])
	}
}
