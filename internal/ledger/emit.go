package ledger

import (
	"context"
	"time"

	"github.com/bobmcallan/satellites/internal/hubemit"
)

// EventKind values published to the websocket hub on ledger mutations.
// Slice 10.3 (story_7ed84379).
const (
	EventKindAppended      = "ledger.append"
	EventKindDereferenced  = "ledger.dereference"
	EventKindStoryActivity = "story.activity.append"
	topicPrefix            = "ws:"
)

// DefaultStoryActivityKindTags enumerates the kind:* tag values that
// trigger an EventKindStoryActivity emit in addition to the generic
// ledger.append. Mirrors portal.DefaultStoryActivityKinds. Duplicated
// here because internal/ledger imports cannot reach into
// internal/portal without flipping the dependency direction. The
// portal package owns the canonical default for backfill; the emit
// path uses this hardcoded mirror so the publish hot-path stays free
// of cross-package churn. Operators wanting to extend the set in
// production override the project-scope KV `story_activity.kinds`
// surface — that override is read by the backfill path. v1: emit
// covers every default kind; KV override applies to backfill only
// (sty_e55f335e tradeoff to keep emit synchronous and KV-read free).
var DefaultStoryActivityKindTags = []string{
	"kind:workflow-claim",
	"kind:plan",
	"kind:plan-approved",
	"kind:plan-amend",
	"kind:role-grant",
	"kind:agent-compose",
	"kind:action-claim",
	"kind:close-request",
	"kind:evidence",
	"kind:artifact",
	"kind:review-question",
	"kind:review-response",
	"kind:verdict",
}

// activityKindSet is the membership-test version of
// DefaultStoryActivityKindTags. Built once at package init so the
// emit hot-path is a single map lookup per tag.
var activityKindSet = func() map[string]struct{} {
	out := make(map[string]struct{}, len(DefaultStoryActivityKindTags))
	for _, k := range DefaultStoryActivityKindTags {
		out[k] = struct{}{}
	}
	return out
}()

// emitAppended publishes a ledger.append event for the supplied entry.
// The call is wrapped in a recover so a panicking subscriber cannot
// abort the caller's mutation — emits are advisory.
func emitAppended(ctx context.Context, p hubemit.Publisher, entry LedgerEntry) {
	if p == nil || entry.WorkspaceID == "" {
		return
	}
	defer func() { _ = recover() }()
	payload := map[string]any{
		"workspace_id": entry.WorkspaceID,
		"project_id":   entry.ProjectID,
		"ledger_id":    entry.ID,
		"type":         entry.Type,
		"tags":         entry.Tags,
	}
	if entry.StoryID != nil {
		payload["story_id"] = *entry.StoryID
	}
	if entry.ContractID != nil {
		payload["contract_id"] = *entry.ContractID
	}
	p.Publish(ctx, topicPrefix+entry.WorkspaceID, EventKindAppended, entry.WorkspaceID, payload)

	// Activity panel fan-out (sty_e55f335e). Re-emit the same payload
	// under EventKindStoryActivity when the row is story-scoped AND
	// carries a kind:* tag in the activity set. The activity panel
	// subscribes to this kind specifically so it never has to
	// client-filter the higher-volume ledger.append stream.
	if entry.StoryID == nil {
		return
	}
	matchedKind, ok := activityKindForTags(entry.Tags)
	if !ok {
		return
	}
	// Build a richer payload than the generic ledger.append. The
	// activity panel needs `content` + `created_at` + the matched
	// `kind` to render a row without a refetch. Mirrors the panel's
	// view-model so the JS handler can append directly.
	activityPayload := map[string]any{
		"workspace_id": entry.WorkspaceID,
		"project_id":   entry.ProjectID,
		"ledger_id":    entry.ID,
		"type":         entry.Type,
		"tags":         entry.Tags,
		"content":      entry.Content,
		"kind":         matchedKind,
		"story_id":     *entry.StoryID,
		"created_at":   entry.CreatedAt.UTC().Format(time.RFC3339),
	}
	if entry.ContractID != nil {
		activityPayload["contract_id"] = *entry.ContractID
	}
	p.Publish(ctx, topicPrefix+entry.WorkspaceID, EventKindStoryActivity, entry.WorkspaceID, activityPayload)
}

// activityKindForTags returns the first kind:* tag in tags whose value
// is in the activity-emit allowlist, plus an ok flag. Cost is
// O(len(tags)).
func activityKindForTags(tags []string) (string, bool) {
	for _, t := range tags {
		if _, ok := activityKindSet[t]; ok {
			return t, true
		}
	}
	return "", false
}

// emitDereferenced publishes a ledger.dereference event for the target
// row id that has just been flipped to status=dereferenced.
func emitDereferenced(ctx context.Context, p hubemit.Publisher, workspaceID, ledgerID, reason string) {
	if p == nil || workspaceID == "" {
		return
	}
	defer func() { _ = recover() }()
	payload := map[string]any{
		"workspace_id": workspaceID,
		"ledger_id":    ledgerID,
		"reason":       reason,
	}
	p.Publish(ctx, topicPrefix+workspaceID, EventKindDereferenced, workspaceID, payload)
}
