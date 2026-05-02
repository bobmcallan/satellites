package story

import (
	"context"

	"github.com/bobmcallan/satellites/internal/hubemit"
)

const (
	eventKindPrefix = "story."
	topicPrefix     = "ws:"
)

// emitStatus publishes a story.<status> event for the post-mutation row.
// Advisory: subscriber panics are recovered. Payload carries the row
// fields the panel needs to render without a follow-up fetch — Create
// emits give the panel enough to append a fresh row directly (sty_1ff1065a).
func emitStatus(ctx context.Context, p hubemit.Publisher, s Story) {
	if p == nil || s.WorkspaceID == "" || s.Status == "" {
		return
	}
	defer func() { _ = recover() }()
	tags := s.Tags
	if tags == nil {
		tags = []string{}
	}
	payload := map[string]any{
		"workspace_id": s.WorkspaceID,
		"project_id":   s.ProjectID,
		"story_id":     s.ID,
		"title":        s.Title,
		"status":       s.Status,
		"priority":     s.Priority,
		"category":     s.Category,
		"tags":         tags,
		"updated_at":   s.UpdatedAt,
	}
	p.Publish(ctx, topicPrefix+s.WorkspaceID, eventKindPrefix+s.Status, s.WorkspaceID, payload)
}
