// Task lifecycle hooks (epic:project-tasks). A top-level task (type:task, tsk_)
// is a re-runnable, reviewer-gated project work item — a peer to stories. It
// shares the ledger primitive but emits its OWN kinds (task_created /
// task_updated) and deliberately does NOT fire the story reviewer/summary
// signals: a task's process is its satellites-task-* workflow, not the story
// reviewer dispatch.
package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
)

// taskLedgerPayload is the minimal structured record stamped onto a task's
// create/update ledger rows.
type taskLedgerPayload struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Priority string `json:"priority,omitempty"`
	ParentID string `json:"parent_id,omitempty"`
}

func taskPayload(d document.Document) []byte {
	p, _ := json.Marshal(taskLedgerPayload{
		ID: d.ID, Title: d.Name, Status: d.Status,
		Priority: d.Priority, ParentID: d.ParentID,
	})
	return p
}

// taskAfterCreate records a task_created ledger row. Unlike storyAfterCreate it
// runs no reviewer dispatch and no summary regen — those are story signals.
func taskAfterCreate(ctx context.Context, d document.Document) {
	appendTaskLedger(ctx, d, ledger.KindTaskCreated, "create")
}

// taskAfterUpdate records a task_updated ledger row.
func taskAfterUpdate(ctx context.Context, d document.Document) {
	appendTaskLedger(ctx, d, ledger.KindTaskUpdated, "update")
}

func appendTaskLedger(ctx context.Context, d document.Document, kind, op string) {
	if ledgerStore == nil {
		return
	}
	if _, err := ledgerStore.Append(ctx, ledger.AppendInput{
		StoryID: d.ID, // the ledger key is a plain id (no FK) — a tsk_ id works as-is
		Kind:    kind,
		Actor:   actorFromContext(ctx),
		Payload: taskPayload(d),
	}, time.Now().UTC()); err != nil {
		// The row write already landed; surface and continue, mirroring
		// storyAfterCreate's best-effort ledger semantics.
		fmt.Printf("document_upsert: task ledger append (%s): %v\n", op, err)
	}
}
