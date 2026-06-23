package server

import (
	"context"
	"encoding/json"
	"html/template"
	"strings"

	"github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/verb"
)

// project_codegraph.go surfaces a project's published codegraph (B2,
// epic:codegraph-usability) on the project page. The `type:codegraph` document
// (published by the Codegraph task, B1) is NOT a `type:document` row, so it does not
// appear in the documents panel — this fetches it directly and renders its body into
// a dedicated card, where codegraph-init.js turns the embedded `language-codegraph`
// JSON block into an interactive graph. Read-only and best-effort: any miss/error
// yields an empty card (graceful absence), never a blanked page.

// gatherCodegraph resolves the newest `type:codegraph` document for a project and
// returns its body rendered as safe markdown, plus whether one was found. A missing
// document, an empty list, or any verb error returns ("", false) — the caller renders
// no card. The body carries the B1 `codegraph` fenced block the viewer consumes.
func gatherCodegraph(ctx context.Context, projectID string) (template.HTML, bool) {
	listReq, _ := json.Marshal(verb.DocumentListRequest{
		Type:      "document",
		ProjectID: projectID,
		Tags:      []string{"type:codegraph"},
		Limit:     50,
	})
	raw, err := verb.Dispatch(ctx, "document_list", listReq)
	if err != nil {
		arbor.WarnCtx(ctx, "project_codegraph: list", "id", projectID, "err", err)
		return "", false
	}
	var resp verb.DocumentListResponse
	if err := json.Unmarshal(raw, &resp); err != nil || len(resp.Items) == 0 {
		return "", false
	}
	// Newest by updated_at — the current-state snapshot (the task overwrites one doc,
	// but guard against duplicates by picking the freshest).
	newest := resp.Items[0]
	for _, d := range resp.Items[1:] {
		if d.UpdatedAt.After(newest.UpdatedAt) {
			newest = d
		}
	}
	body, err := dispatchStoryBody(ctx, newest.ID)
	if err != nil {
		arbor.WarnCtx(ctx, "project_codegraph: body", "id", newest.ID, "err", err)
		return "", false
	}
	if strings.TrimSpace(body) == "" {
		return "", false
	}
	return renderMarkdown(body), true
}
