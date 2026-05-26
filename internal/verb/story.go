// Internal story envelope + after-write hooks.
//
// Post-unification (sty_0dd71f79) the public verb surface is the four
// document_* verbs — there are no story_* verbs. This file holds the
// private machinery that document_upsert uses when the row being
// written is type='story':
//
//   - StoryEnvelope: the JSON shape the reviewer + summary + ledger
//     consumers expect (title/body/tags/…). Built from a document.Document
//     so reviewer prompt markdown doesn't need to change.
//   - storyAfterCreate / storyAfterUpdate: dispatch reviewers, append
//     a ledger entry, kick a summary regen. Same chain story_create /
//     story_update used to fire before unification.

package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
)

// StoryEnvelope is the JSON shape passed to ledger payloads, reviewers,
// and the summary regeneration hook. Field names mirror the pre-
// unification story.Story so reviewer prompts that reference {title,
// body, status, …} keep resolving against the same keys.
type StoryEnvelope struct {
	ID                 string     `json:"id"`
	ProjectID          string     `json:"project_id"`
	ParentID           string     `json:"parent_id,omitempty"`
	Title              string     `json:"title"`
	Body               string     `json:"body,omitempty"`
	AcceptanceCriteria string     `json:"acceptance_criteria,omitempty"`
	Status             string     `json:"status"`
	Priority           string     `json:"priority"`
	Category           string     `json:"category"`
	Tags               []string   `json:"tags"`
	Summary            string     `json:"summary,omitempty"`
	SummaryUpdatedAt   *time.Time `json:"summary_updated_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// NewStoryEnvelope projects a document.Document (which carries unified
// fields) into the story-shaped envelope downstream consumers want.
func NewStoryEnvelope(d document.Document, body string) StoryEnvelope {
	tags := d.Tags
	if tags == nil {
		tags = []string{}
	}
	return StoryEnvelope{
		ID:                 d.ID,
		ProjectID:          d.ProjectID,
		ParentID:           d.ParentID,
		Title:              d.Name,
		Body:               body,
		AcceptanceCriteria: d.AcceptanceCriteria,
		Status:             d.Status,
		Priority:           d.Priority,
		Category:           d.Category,
		Tags:               tags,
		Summary:            d.Summary,
		SummaryUpdatedAt:   d.SummaryUpdatedAt,
		CreatedAt:          d.CreatedAt,
		UpdatedAt:          d.UpdatedAt,
	}
}

// storyAfterCreate is the post-create hook chain document_upsert fires
// when it inserts a type='story' row. Appends a ledger entry, fires the
// reviewer registry, kicks off summary regen.
func storyAfterCreate(ctx context.Context, d document.Document, body string) {
	env := NewStoryEnvelope(d, body)
	if ledgerStore != nil {
		payload, _ := json.Marshal(env)
		if _, err := ledgerStore.Append(ctx, ledger.AppendInput{
			StoryID: d.ID,
			Kind:    ledger.KindStoryCreated,
			Actor:   actorFromContext(ctx),
			Payload: payload,
		}, time.Now().UTC()); err != nil {
			// Ledger append failures are surfaced to the operator
			// via the verb response, but the document write has
			// already succeeded — log and continue.
			fmt.Printf("document_upsert: ledger append (create): %v\n", err)
		}
		dispatchSummaryRegen(ctx, d.ID)
	}
	dispatchReviewers(ctx, env)
}

// storyAfterUpdate is the post-update hook chain document_upsert fires
// when it patches a type='story' row.
func storyAfterUpdate(ctx context.Context, before StoryEnvelope, d document.Document, body string) {
	after := NewStoryEnvelope(d, body)
	if ledgerStore != nil {
		diff := computeStoryDiff(before, after)
		if len(diff) > 0 {
			payload, _ := json.Marshal(diff)
			if _, err := ledgerStore.Append(ctx, ledger.AppendInput{
				StoryID: d.ID,
				Kind:    ledger.KindStoryUpdated,
				Actor:   actorFromContext(ctx),
				Payload: payload,
			}, time.Now().UTC()); err != nil {
				fmt.Printf("document_upsert: ledger append (update): %v\n", err)
			}
			dispatchSummaryRegen(ctx, d.ID)
		}
	}
	dispatchReviewers(ctx, after)
}

// computeStoryDiff returns a {field: {before, after}} map for each
// field whose value changed between two envelopes.
func computeStoryDiff(before, after StoryEnvelope) map[string]map[string]any {
	diff := map[string]map[string]any{}
	if before.ParentID != after.ParentID {
		diff["parent_id"] = map[string]any{"before": before.ParentID, "after": after.ParentID}
	}
	if before.Title != after.Title {
		diff["title"] = map[string]any{"before": before.Title, "after": after.Title}
	}
	if before.Body != after.Body {
		diff["body"] = map[string]any{"before": before.Body, "after": after.Body}
	}
	if before.AcceptanceCriteria != after.AcceptanceCriteria {
		diff["acceptance_criteria"] = map[string]any{"before": before.AcceptanceCriteria, "after": after.AcceptanceCriteria}
	}
	if before.Status != after.Status {
		diff["status"] = map[string]any{"before": before.Status, "after": after.Status}
	}
	if before.Priority != after.Priority {
		diff["priority"] = map[string]any{"before": before.Priority, "after": after.Priority}
	}
	if before.Category != after.Category {
		diff["category"] = map[string]any{"before": before.Category, "after": after.Category}
	}
	if !stringSlicesEqual(before.Tags, after.Tags) {
		diff["tags"] = map[string]any{"before": before.Tags, "after": after.Tags}
	}
	return diff
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
