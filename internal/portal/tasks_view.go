// Tasks-view composite for slice 11.2 (story_f2d71c27). Three-column
// board of tasks per docs/ui-design.md §2.3 — in_flight, enqueued,
// recently closed — plus a per-task drawer payload (`/api/tasks/{id}`).
package portal

import (
	"context"
	"sort"
	"time"

	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/task"
)

// closedColumnLimit caps the recently-closed column. Older closed tasks
// fall off the live view but remain queryable via task_list.
const closedColumnLimit = 50

// tasksComposite is the view-model for the task-queue page. Three
// arrays mirror the §2.3 columns; the SSR template + JSON endpoint
// render from the same shape.
type tasksComposite struct {
	InFlight []taskCard `json:"in_flight"`
	Enqueued []taskCard `json:"enqueued"`
	Closed   []taskCard `json:"recently_closed"`
}

// taskCard is one row in any of the three columns.
type taskCard struct {
	ID          string `json:"id"`
	Origin      string `json:"origin"`
	Status      string `json:"status"`
	Priority    string `json:"priority"`
	StoryID     string `json:"story_id,omitempty"`
	ClaimedBy   string `json:"claimed_by,omitempty"`
	ClaimedAt   string `json:"claimed_at,omitempty"`
	CompletedAt string `json:"completed_at,omitempty"`
	Outcome     string `json:"outcome,omitempty"`
	CreatedAt   string `json:"created_at"`
}

// taskDrawer is the per-task detail composite returned by
// `/api/tasks/{id}`. Carries the trigger blob + a bounded ledger trail
// rooted at the task.LedgerRootID (when present). sty_509a46fa
// retired Task.Payload; artifact content lives on linked ledger rows.
type taskDrawer struct {
	Task     taskCard        `json:"task"`
	Trigger  string          `json:"trigger,omitempty"`
	Excerpts []ledgerExcerpt `json:"ledger_excerpts"`
}

// buildTasksComposite assembles the three-column composite for the
// caller's workspace memberships. nil store gracefully degrades to an
// empty composite (the SSR template renders empty-state copy).
func buildTasksComposite(ctx context.Context, tasks task.Store, memberships []string) tasksComposite {
	if tasks == nil {
		return tasksComposite{}
	}
	c := tasksComposite{}
	if rows, err := tasks.List(ctx, task.ListOptions{Status: task.StatusInFlight, Limit: 200}, memberships); err == nil {
		c.InFlight = append(c.InFlight, taskCardsFor(rows)...)
	}
	if rows, err := tasks.List(ctx, task.ListOptions{Status: task.StatusClaimed, Limit: 200}, memberships); err == nil {
		c.InFlight = append(c.InFlight, taskCardsFor(rows)...)
	}
	if rows, err := tasks.List(ctx, task.ListOptions{Status: task.StatusEnqueued, Limit: 200}, memberships); err == nil {
		c.Enqueued = append(c.Enqueued, taskCardsFor(rows)...)
	}
	if rows, err := tasks.List(ctx, task.ListOptions{Status: task.StatusClosed, Limit: closedColumnLimit}, memberships); err == nil {
		closed := taskCardsFor(rows)
		sort.SliceStable(closed, func(i, j int) bool { return closed[i].CompletedAt > closed[j].CompletedAt })
		c.Closed = closed
	}
	return c
}

// taskCardsFor projects task.Task rows into the view-model.
func taskCardsFor(rows []task.Task) []taskCard {
	out := make([]taskCard, 0, len(rows))
	for _, t := range rows {
		card := taskCard{
			ID:        t.ID,
			Origin:    t.Origin,
			Status:    t.Status,
			Priority:  t.Priority,
			StoryID:   t.StoryID,
			ClaimedBy: t.ClaimedBy,
			Outcome:   t.Outcome,
			CreatedAt: t.CreatedAt.UTC().Format(time.RFC3339),
		}
		if t.ClaimedAt != nil {
			card.ClaimedAt = t.ClaimedAt.UTC().Format(time.RFC3339)
		}
		if t.CompletedAt != nil {
			card.CompletedAt = t.CompletedAt.UTC().Format(time.RFC3339)
		}
		out = append(out, card)
	}
	return out
}

// buildTaskDrawer assembles the per-task drawer payload. Reads the
// task row, then the ledger excerpts rooted at the task's
// LedgerRootID (when set) so the drawer shows the audit trail.
func buildTaskDrawer(ctx context.Context, tasks task.Store, ledgerStore ledger.Store, projectID, taskID string, memberships []string) (taskDrawer, error) {
	t, err := tasks.GetByID(ctx, taskID, memberships)
	if err != nil {
		return taskDrawer{}, err
	}
	cards := taskCardsFor([]task.Task{t})
	if len(cards) == 0 {
		return taskDrawer{}, ErrDrawerEmpty
	}
	d := taskDrawer{
		Task:    cards[0],
		Trigger: string(t.Trigger),
	}
	if ledgerStore != nil && t.LedgerRootID != "" {
		rows, err := ledgerStore.Recall(ctx, t.LedgerRootID, memberships)
		if err == nil {
			d.Excerpts = excerptsFromLedgerRows(rows)
		}
	}
	return d, nil
}

// excerptsFromLedgerRows projects ledger rows into the same shape used
// by the story-view excerpts panel so the template can render a
// shared partial.
func excerptsFromLedgerRows(rows []ledger.LedgerEntry) []ledgerExcerpt {
	out := make([]ledgerExcerpt, 0, len(rows))
	for _, r := range rows {
		out = append(out, ledgerExcerpt{
			ID:        r.ID,
			Type:      r.Type,
			Tags:      r.Tags,
			Content:   truncate(r.Content, 240),
			CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out
}

// ErrDrawerEmpty is returned by buildTaskDrawer when the task lookup
// succeeded but produced no card rows (defensive: should not occur in
// practice).
var ErrDrawerEmpty = errDrawerEmpty{}

type errDrawerEmpty struct{}

func (errDrawerEmpty) Error() string { return "portal: drawer projection empty" }
