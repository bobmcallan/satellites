// Story summary verbs — manual regeneration trigger. The summary
// is also regenerated automatically on every ledger append for a
// story (see summary_hook.go).

package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type StorySummaryRegenerateRequest struct {
	StoryID string `json:"story_id"`
}

type StorySummaryRegenerateResponse struct {
	Dispatched bool `json:"dispatched"`
}

func init() {
	Register(&Verb{
		Name:        "story_summary_regenerate",
		Description: "Trigger an immediate regeneration of a story's summary. The regeneration runs synchronously in this call (unlike the auto-trigger from ledger appends, which is async).",
		Invoke:      invokeStorySummaryRegenerate,
	})
}

func invokeStorySummaryRegenerate(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var req StorySummaryRegenerateRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("story_summary_regenerate: bad request: %w", err)
		}
	}
	if strings.TrimSpace(req.StoryID) == "" {
		return nil, fmt.Errorf("story_summary_regenerate: story_id required")
	}
	if reviewerRegistry == nil {
		return nil, fmt.Errorf("story_summary_regenerate: reviewer registry not configured")
	}
	def, ok := reviewerRegistry.Defs[SummaryReviewerName]
	if !ok || !def.Enabled {
		return nil, fmt.Errorf("story_summary_regenerate: %s reviewer not enabled", SummaryReviewerName)
	}
	if reviewerRegistry.Client == nil {
		return nil, fmt.Errorf("story_summary_regenerate: reviewer client not configured")
	}
	// Synchronous — the operator asked, so we run inline and the
	// response reflects the post-regen state. (The auto-trigger
	// from ledger appends stays async.)
	regenerateSummary(ctx, req.StoryID)
	return json.Marshal(StorySummaryRegenerateResponse{Dispatched: true})
}
