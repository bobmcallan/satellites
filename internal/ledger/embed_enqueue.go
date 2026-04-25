package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/bobmcallan/satellites/internal/task"
)

// EmbedTaskKind tags an embed-ledger task in its Payload.
const EmbedTaskKind = "embed-ledger"

// EmbedPayload is the Task.Payload shape for embed-ledger tasks.
type EmbedPayload struct {
	Kind        string `json:"kind"`
	LedgerID    string `json:"ledger_id"`
	WorkspaceID string `json:"workspace_id"`
}

// minContentForEmbed is the noise floor below which embedding is wasteful.
const minContentForEmbed = 32

// minKVContent is a higher bar applied to Type=kv rows — single-value KV
// rows ("1", "true", short hashes) carry no semantic signal worth embedding.
const minKVContent = 64

// EnqueueAppend writes an embed-ledger task to the queue if the row is
// worth embedding. Skip rules:
//
//   - Type=kv with len(Content) < 64 — single-value KV rows.
//   - Type=action_claim — JSON-shaped permission lists.
//   - Empty Content.
//   - Status != active (already-archived rows skip).
//
// Returns nil + nil when skipped; only real Enqueue failures error.
func EnqueueAppend(ctx context.Context, taskStore task.Store, entry LedgerEntry, now time.Time) (*task.Task, error) {
	if taskStore == nil {
		return nil, nil
	}
	if !shouldEmbed(entry) {
		return nil, nil
	}
	payload, err := json.Marshal(EmbedPayload{
		Kind:        EmbedTaskKind,
		LedgerID:    entry.ID,
		WorkspaceID: entry.WorkspaceID,
	})
	if err != nil {
		return nil, err
	}
	t, err := taskStore.Enqueue(ctx, task.Task{
		WorkspaceID: entry.WorkspaceID,
		ProjectID:   entry.ProjectID,
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

func shouldEmbed(entry LedgerEntry) bool {
	if entry.Status != StatusActive {
		return false
	}
	if len(entry.Content) == 0 {
		return false
	}
	if entry.Type == TypeKV && len(entry.Content) < minKVContent {
		return false
	}
	if entry.Type == TypeActionClaim {
		return false
	}
	if len(entry.Content) < minContentForEmbed {
		return false
	}
	return true
}

// ParseEmbedPayload decodes an embed-ledger task payload. Returns
// (zero, false) on any decoding failure or kind mismatch.
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
	if p.LedgerID == "" {
		return EmbedPayload{}, false
	}
	return p, true
}

// ErrEmbedFailed wraps a chunk-store or embedder failure during the
// worker's embed flow.
var ErrEmbedFailed = errors.New("ledger: embedding failed")
