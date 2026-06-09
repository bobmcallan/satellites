package server

import "testing"

// TestStatusRank pins the workflow-ordered status rank (order:3): a status is
// ranked by its index in the story's OWN ## Workflow states, so a custom
// lifecycle sorts by its real position; an off-workflow status or a body with
// no workflow block sinks to the sentinel.
func TestStatusRank(t *testing.T) {
	body := "# A story\n\n## Workflow\n\n```yaml\n" +
		"states:\n  - backlog\n  - in_progress\n  - integration_review\n  - done\n" +
		"transitions:\n" +
		"  - {from: backlog, to: in_progress, reviewer_skill: \"a\"}\n" +
		"  - {from: in_progress, to: integration_review, reviewer_skill: \"b\"}\n" +
		"  - {from: integration_review, to: done, reviewer_skill: \"c\"}\n" +
		"```\n"

	cases := []struct {
		name, status string
		want         int
	}{
		{"first state ranks 0", "backlog", 0},
		{"custom mid state ranks by position", "integration_review", 2},
		{"terminal ranks last", "done", 3},
		{"off-workflow status sinks", "frobnicate", statusRankUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := statusRank(body, tc.status); got != tc.want {
				t.Fatalf("statusRank(%q) = %d, want %d", tc.status, got, tc.want)
			}
		})
	}

	if got := statusRank("no workflow block here", "backlog"); got != statusRankUnknown {
		t.Fatalf("no-workflow body: got %d, want sentinel %d", got, statusRankUnknown)
	}
}
