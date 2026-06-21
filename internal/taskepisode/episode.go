// Package taskepisode holds the read-time execution-episode projection for a
// task's ledger (epic:project-tasks re-runnable tasks). It is a pure, dependency-
// free function so BOTH the CLI (`satellites task executions`) and the server's
// operator UI (the task detail Executions view, epic:workflow-steps order-6)
// group a task's runs the SAME way — one projection, no duplication.
package taskepisode

import "time"

// Row is one ledger row reduced to what episode projection needs: its kind, the
// to_status (only meaningful for a status_transition row), and when it landed.
type Row struct {
	Kind    string
	To      string
	Created time.Time
}

// Episode is one execution of a task — the span from a status_transition into
// `running` to the next transition into a terminal state (complete / cancelled).
// Run is the 1-based episode index (run-1, run-2 …), derived from the
// authoritative status_transition timeline rather than stored, so every ledger
// row (including agent body-patches the gate never sees) groups correctly. End
// is the zero time while the run is still open; EndTo is the terminal status
// that closed it ("" while open). Rows counts the ledger rows in the episode.
type Episode struct {
	Run   int
	Start time.Time
	End   time.Time
	EndTo string
	Rows  int
	// StartIdx is the index, in the rows slice passed to Project, of this
	// episode's opening (status_transition → running) row. Because a caller
	// builds the rows slice 1:1 and in order from its source entries, the
	// episode's entries are exactly source[StartIdx : StartIdx+Rows] — so the UI
	// can slice a task's ledger rows per episode by the SAME projection, with no
	// second walk. Rows between a terminal transition and the next `running` (a
	// re-arm's review_requested, say) fall in no episode's range.
	StartIdx int
}

// Open reports whether the episode is still running (not yet closed by a
// terminal transition).
func (e Episode) Open() bool { return e.End.IsZero() }

// Project derives execution episodes from a task's ledger rows (oldest-first). A
// status_transition into `running` opens an episode; the next transition into a
// terminal state (complete/cancelled) closes the open one. Every row in between
// counts toward the open episode. Pure + deterministic so it is unit-testable
// and so the CLI and the UI group identically.
func Project(rows []Row) []Episode {
	var episodes []Episode
	cur := -1
	for i, e := range rows {
		if e.Kind == "status_transition" && e.To == "running" {
			episodes = append(episodes, Episode{Run: len(episodes) + 1, Start: e.Created, Rows: 1, StartIdx: i})
			cur = len(episodes) - 1
			continue
		}
		if cur >= 0 {
			episodes[cur].Rows++
			if e.Kind == "status_transition" && (e.To == "complete" || e.To == "cancelled") {
				episodes[cur].End = e.Created
				episodes[cur].EndTo = e.To
				cur = -1
			}
		}
	}
	return episodes
}
