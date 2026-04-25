package document

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/bobmcallan/satellites/internal/task"
)

// EmbedTaskKind tags an embed-document task in its Payload. Workers
// scan task rows and filter on this kind before processing.
const EmbedTaskKind = "embed-document"

// EmbedPayload is the Task.Payload shape for embed-document tasks.
type EmbedPayload struct {
	Kind        string `json:"kind"`
	DocumentID  string `json:"document_id"`
	WorkspaceID string `json:"workspace_id"`
}

// minBodyForEmbed is the threshold below which a document body is too
// small to chunk + embed meaningfully. Trivial bodies (auto-generated
// stubs, smoke tests) skip the worker pipeline.
const minBodyForEmbed = 32

// EnqueueIngest writes an embed-document task to the queue if the doc's
// body is large enough to be worth embedding. Returns nil + nil when the
// skip rules apply (caller treats that as a no-op success). Returns an
// error only on a real Enqueue failure — callers should NOT block the
// underlying store mutation on this; the queue is durable but advisory.
func EnqueueIngest(ctx context.Context, taskStore task.Store, doc Document, now time.Time) (*task.Task, error) {
	if taskStore == nil {
		return nil, nil
	}
	if !shouldEmbed(doc) {
		return nil, nil
	}
	payload, err := json.Marshal(EmbedPayload{
		Kind:        EmbedTaskKind,
		DocumentID:  doc.ID,
		WorkspaceID: doc.WorkspaceID,
	})
	if err != nil {
		return nil, err
	}
	t, err := taskStore.Enqueue(ctx, task.Task{
		WorkspaceID: doc.WorkspaceID,
		ProjectID:   stringDeref(doc.ProjectID),
		Origin:      task.OriginEvent,
		Priority:    task.PriorityMedium,
		Status:      task.StatusEnqueued,
		Payload:     payload,
	}, now)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// shouldEmbed gates the EnqueueIngest call. Empty body, bodies below the
// noise threshold, and archived rows skip embedding.
func shouldEmbed(doc Document) bool {
	if doc.Status != StatusActive {
		return false
	}
	if len(doc.Body) < minBodyForEmbed {
		return false
	}
	return true
}

// stringDeref returns *p or empty string when p is nil.
func stringDeref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// ParseEmbedPayload decodes an embed-document task payload. Returns
// (zero, false) on any decoding failure or when kind doesn't match —
// workers fall through to the next task in their queue.
func ParseEmbedPayload(payload []byte) (EmbedPayload, bool) {
	if len(payload) == 0 {
		return EmbedPayload{}, false
	}
	var p EmbedPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return EmbedPayload{}, false
	}
	if p.Kind != EmbedTaskKind {
		return EmbedPayload{}, false
	}
	if p.DocumentID == "" {
		return EmbedPayload{}, false
	}
	return p, true
}

// ErrEmbedFailed wraps a chunk-store or embedder failure during the
// worker's embed flow.
var ErrEmbedFailed = errors.New("document: embedding failed")
