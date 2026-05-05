// Ledger inspection composite for slice 11.3 (story_a9f8be3c). Builds
// the workspace-scoped ledger view per docs/ui-design.md §2.4 — search,
// filter sidebar, expand-row payloads, plus pagination metadata for
// the "N new rows" pill. SSR + JSON share this shape.
package portal

import (
	"context"
	"net/http"
	"strings"

	"github.com/bobmcallan/satellites/internal/ledger"
)

const ledgerDefaultLimit = 50

// ledgerComposite is the view-model for the ledger inspection page.
type ledgerComposite struct {
	Rows    []ledgerRowView `json:"rows"`
	Filters ledgerFilters   `json:"filters"`
	Total   int             `json:"total"`
}

// ledgerRowView is one rendered ledger row. Carries `Structured` as a
// raw string so the template can `<pre>`-print it (the JSON endpoint
// keeps the original bytes for client-side parsing).
type ledgerRowView struct {
	ID         string   `json:"id"`
	Type       string   `json:"type"`
	Tags       []string `json:"tags,omitempty"`
	StoryID    string   `json:"story_id,omitempty"`
	Durability string   `json:"durability"`
	SourceType string   `json:"source_type"`
	Status     string   `json:"status"`
	CreatedAt  string   `json:"created_at"`
	CreatedBy  string   `json:"created_by"`
	Content    string   `json:"content"`
	Structured string   `json:"structured,omitempty"`
}

// ledgerFilters echoes the active filter state so the template can
// render the sidebar with the correct selections.
type ledgerFilters struct {
	Query      string   `json:"query,omitempty"`
	Type       string   `json:"type,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	StoryID    string   `json:"story_id,omitempty"`
	Durability string   `json:"durability,omitempty"`
	SourceType string   `json:"source_type,omitempty"`
	Status     string   `json:"status,omitempty"`
}

// parseLedgerFilters reads `?q=`, `?type=`, `?tag=`, `?story_id=`,
// `?durability=`, `?source_type=`, `?status=` from the request.
// `tag` may be repeated or comma-separated.
func parseLedgerFilters(r *http.Request) ledgerFilters {
	q := r.URL.Query()
	tags := make([]string, 0)
	for _, v := range q["tag"] {
		for _, t := range strings.Split(v, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				tags = append(tags, t)
			}
		}
	}
	return ledgerFilters{
		Query:      strings.TrimSpace(q.Get("q")),
		Type:       strings.TrimSpace(q.Get("type")),
		Tags:       tags,
		StoryID:    strings.TrimSpace(q.Get("story_id")),
		Durability: strings.TrimSpace(q.Get("durability")),
		SourceType: strings.TrimSpace(q.Get("source_type")),
		Status:     strings.TrimSpace(q.Get("status")),
	}
}

// buildLedgerComposite reads a project's ledger rows under the active
// filters. Free-text query routes through ledger.Search; structured
// filters use ledger.List.
func buildLedgerComposite(ctx context.Context, store ledger.Store, projectID string, f ledgerFilters, memberships []string) ledgerComposite {
	if store == nil {
		return ledgerComposite{Filters: f}
	}
	listOpts := ledger.ListOptions{
		Type:       f.Type,
		StoryID:    f.StoryID,
		Tags:       f.Tags,
		Durability: f.Durability,
		SourceType: f.SourceType,
		Status:     f.Status,
		Limit:      ledgerDefaultLimit,
	}
	var rows []ledger.LedgerEntry
	var err error
	if f.Query != "" {
		rows, err = store.Search(ctx, projectID, ledger.SearchOptions{
			ListOptions: listOpts,
			Query:       f.Query,
		}, memberships)
	} else {
		rows, err = store.List(ctx, projectID, listOpts, memberships)
	}
	if err != nil {
		return ledgerComposite{Filters: f}
	}
	out := make([]ledgerRowView, 0, len(rows))
	for _, r := range rows {
		out = append(out, ledgerRowViewFor(r))
	}
	return ledgerComposite{Rows: out, Filters: f, Total: len(out)}
}

// ledgerRowViewFor projects a ledger.LedgerEntry into the row
// view-model.
func ledgerRowViewFor(r ledger.LedgerEntry) ledgerRowView {
	v := ledgerRowView{
		ID:         r.ID,
		Type:       r.Type,
		Tags:       r.Tags,
		Durability: r.Durability,
		SourceType: r.SourceType,
		Status:     r.Status,
		CreatedAt:  r.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		CreatedBy:  r.CreatedBy,
		Content:    r.Content,
	}
	if r.StoryID != nil {
		v.StoryID = *r.StoryID
	}
	if len(r.Structured) > 0 {
		v.Structured = string(r.Structured)
	}
	return v
}
