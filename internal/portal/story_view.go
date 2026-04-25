// Story-view composite builder for slice 11.1 (story_3b450d9e). Pulls
// the five panels described in docs/ui-design.md §2.2 — scope/source
// docs / contract-instance timeline / reviewer verdicts / repo
// provenance — into one struct so the SSR template and the JSON
// composite endpoint render from the same shape.
package portal

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/story"
)

// excerptLimit caps the ledger-excerpts panel; older rows fall off the
// view (still queryable via the full ledger inspection page).
const excerptLimit = 50

// commitTagKind identifies the ledger rows the repo-provenance panel
// reads. The emitter that writes these rows is a follow-up story; the
// panel renders an empty-state until those rows exist.
const commitTagKind = "kind:commit"

// verdictTagKind identifies reviewer-verdict ledger rows written by
// internal/mcpserver/close_handlers.go writeVerdictRow.
const verdictTagKind = "kind:verdict"

// storyComposite is the view-model for the upgraded story view. The
// SSR template renders it directly; the JSON composite endpoint marshals
// it for reconnect refetch.
type storyComposite struct {
	Story      storyRow        `json:"story"`
	SourceDocs []sourceDocLink `json:"source_documents"`
	CIs        []ciCard        `json:"contract_instances"`
	Verdicts   []verdictCard   `json:"verdicts"`
	Commits    []commitCard    `json:"commits"`
	Excerpts   []ledgerExcerpt `json:"ledger_excerpts"`
	Delivery   deliveryStrip   `json:"delivery"`
}

// sourceDocLink is the source-documents-panel view-model. Path is the
// raw value after the `source:` tag prefix; Anchor is the optional
// `#fragment`. Display is the human label.
type sourceDocLink struct {
	Tag     string `json:"tag"`
	Path    string `json:"path"`
	Anchor  string `json:"anchor"`
	Display string `json:"display"`
}

// ciCard is one row in the contract-instance timeline panel.
type ciCard struct {
	ID            string `json:"id"`
	ContractName  string `json:"contract_name"`
	Sequence      int    `json:"sequence"`
	Status        string `json:"status"`
	ClaimedAt     string `json:"claimed_at,omitempty"`
	ClosedAt      string `json:"closed_at,omitempty"`
	PlanLedgerID  string `json:"plan_ledger_id,omitempty"`
	CloseLedgerID string `json:"close_ledger_id,omitempty"`
}

// verdictCard is one reviewer-verdict row scoped to this story.
type verdictCard struct {
	LedgerID     string `json:"ledger_id"`
	CIID         string `json:"contract_instance_id,omitempty"`
	ContractName string `json:"contract_name,omitempty"`
	Verdict      string `json:"verdict,omitempty"`
	Score        int    `json:"score,omitempty"`
	Reasoning    string `json:"reasoning,omitempty"`
	CreatedAt    string `json:"created_at"`
}

// commitCard is one commit linked to this story (via `kind:commit`
// ledger rows). Empty list → panel renders the empty-state copy.
type commitCard struct {
	LedgerID  string `json:"ledger_id"`
	SHA       string `json:"sha,omitempty"`
	Subject   string `json:"subject,omitempty"`
	Author    string `json:"author,omitempty"`
	URL       string `json:"url,omitempty"`
	CreatedAt string `json:"created_at"`
}

// ledgerExcerpt is one row in the bounded ledger-excerpts panel.
type ledgerExcerpt struct {
	ID        string   `json:"id"`
	Type      string   `json:"type"`
	Tags      []string `json:"tags,omitempty"`
	Content   string   `json:"content,omitempty"`
	CreatedAt string   `json:"created_at"`
}

// deliveryStrip is the banner at the top of the page. Resolution is
// drawn from the most recent kind:verdict row whose phase is
// `phase:story_close`, mirroring the lifecycle's terminal review.
type deliveryStrip struct {
	Status     string `json:"status"`
	Resolution string `json:"resolution,omitempty"`
	Verdict    string `json:"verdict,omitempty"`
	Score      int    `json:"score,omitempty"`
	UpdatedAt  string `json:"updated_at"`
}

// buildStoryComposite assembles the composite for storyID. Any nil
// store gracefully degrades (the corresponding panel renders empty).
// memberships scopes every read identically to the existing handler
// surface so cross-workspace requests stay 404-equivalent.
func buildStoryComposite(
	ctx context.Context,
	stories story.Store,
	contracts contract.Store,
	ledgerStore ledger.Store,
	storyID string,
	memberships []string,
) (storyComposite, error) {
	s, err := stories.GetByID(ctx, storyID, memberships)
	if err != nil {
		return storyComposite{}, err
	}
	c := storyComposite{
		Story:      viewStoryRow(s),
		SourceDocs: sourceDocsForStory(s),
		Delivery:   deliveryStrip{Status: s.Status, UpdatedAt: s.UpdatedAt.UTC().Format(time.RFC3339)},
	}

	if contracts != nil {
		cis, err := contracts.List(ctx, storyID, memberships)
		if err == nil {
			c.CIs = ciCardsFor(cis)
		}
	}

	if ledgerStore != nil {
		c.Verdicts = verdictsForStory(ctx, ledgerStore, s.ProjectID, storyID, memberships)
		c.Commits = commitsForStory(ctx, ledgerStore, s.ProjectID, storyID, memberships)
		c.Excerpts = excerptsForStory(ctx, ledgerStore, s.ProjectID, storyID, memberships)
		c.Delivery = applyDeliveryVerdict(c.Delivery, c.Verdicts)
	}

	return c, nil
}

// sourceDocsForStory parses `source:` tags on the story into
// sourceDocLink rows. The tag convention is `source:<path>` with an
// optional `#anchor` fragment, e.g. `source:ui-design.md#story-view`.
func sourceDocsForStory(s story.Story) []sourceDocLink {
	out := make([]sourceDocLink, 0)
	for _, t := range s.Tags {
		if !strings.HasPrefix(t, "source:") {
			continue
		}
		raw := strings.TrimPrefix(t, "source:")
		if raw == "" {
			continue
		}
		path, anchor := raw, ""
		if i := strings.IndexByte(raw, '#'); i >= 0 {
			path = raw[:i]
			anchor = raw[i+1:]
		}
		display := path
		if anchor != "" {
			display = path + " §" + anchor
		}
		out = append(out, sourceDocLink{
			Tag:     t,
			Path:    path,
			Anchor:  anchor,
			Display: display,
		})
	}
	return out
}

// ciCardsFor projects contract.ContractInstance rows into the timeline
// view-model. Sequence is preserved from the store ordering.
func ciCardsFor(cis []contract.ContractInstance) []ciCard {
	out := make([]ciCard, 0, len(cis))
	for _, ci := range cis {
		card := ciCard{
			ID:            ci.ID,
			ContractName:  ci.ContractName,
			Sequence:      ci.Sequence,
			Status:        ci.Status,
			PlanLedgerID:  ci.PlanLedgerID,
			CloseLedgerID: ci.CloseLedgerID,
		}
		if !ci.ClaimedAt.IsZero() {
			card.ClaimedAt = ci.ClaimedAt.UTC().Format(time.RFC3339)
		}
		// Approximate ClosedAt from UpdatedAt when CI is terminal — the
		// CI struct has no dedicated ClosedAt column; the close handler
		// stamps UpdatedAt on the passed transition.
		if ci.Status == contract.StatusPassed && !ci.UpdatedAt.IsZero() {
			card.ClosedAt = ci.UpdatedAt.UTC().Format(time.RFC3339)
		}
		out = append(out, card)
	}
	return out
}

// verdictsForStory pulls the kind:verdict ledger rows for the story,
// newest-first. Each row's Structured payload (when JSON) carries the
// verdict + score + reasoning; we tolerate bare-content rows by leaving
// those fields empty.
func verdictsForStory(ctx context.Context, store ledger.Store, projectID, storyID string, memberships []string) []verdictCard {
	rows, err := store.List(ctx, projectID, ledger.ListOptions{
		StoryID:       storyID,
		Tags:          []string{verdictTagKind},
		IncludeDerefd: true,
		Limit:         ledger.MaxListLimit,
	}, memberships)
	if err != nil {
		return nil
	}
	out := make([]verdictCard, 0, len(rows))
	for _, r := range rows {
		card := verdictCard{
			LedgerID:  r.ID,
			CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339),
		}
		if r.ContractID != nil {
			card.CIID = *r.ContractID
		}
		for _, t := range r.Tags {
			if strings.HasPrefix(t, "phase:") {
				card.ContractName = strings.TrimPrefix(t, "phase:")
			}
		}
		var payload struct {
			Verdict   string `json:"verdict"`
			Score     int    `json:"score"`
			Reasoning string `json:"reasoning"`
		}
		if len(r.Structured) > 0 {
			_ = json.Unmarshal(r.Structured, &payload)
			card.Verdict = payload.Verdict
			card.Score = payload.Score
			card.Reasoning = payload.Reasoning
		}
		if card.Reasoning == "" {
			card.Reasoning = r.Content
		}
		out = append(out, card)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out
}

// commitsForStory pulls kind:commit ledger rows scoped to the story.
// Until the commit-receiver is wired (follow-up story), this will be
// empty and the panel renders the empty-state.
func commitsForStory(ctx context.Context, store ledger.Store, projectID, storyID string, memberships []string) []commitCard {
	rows, err := store.List(ctx, projectID, ledger.ListOptions{
		StoryID: storyID,
		Tags:    []string{commitTagKind},
		Limit:   ledger.MaxListLimit,
	}, memberships)
	if err != nil {
		return nil
	}
	out := make([]commitCard, 0, len(rows))
	for _, r := range rows {
		card := commitCard{
			LedgerID:  r.ID,
			CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339),
		}
		var payload struct {
			SHA     string `json:"sha"`
			Subject string `json:"subject"`
			Author  string `json:"author"`
			URL     string `json:"url"`
		}
		if len(r.Structured) > 0 {
			_ = json.Unmarshal(r.Structured, &payload)
			card.SHA = payload.SHA
			card.Subject = payload.Subject
			card.Author = payload.Author
			card.URL = payload.URL
		}
		if card.Subject == "" {
			card.Subject = r.Content
		}
		out = append(out, card)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out
}

// excerptsForStory pulls a bounded window of all ledger rows scoped to
// the story (any tag). Used for the live-updating excerpts panel.
func excerptsForStory(ctx context.Context, store ledger.Store, projectID, storyID string, memberships []string) []ledgerExcerpt {
	rows, err := store.List(ctx, projectID, ledger.ListOptions{
		StoryID: storyID,
		Limit:   excerptLimit,
	}, memberships)
	if err != nil {
		return nil
	}
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

// applyDeliveryVerdict folds the most recent story_close verdict into
// the delivery strip. When no story_close verdict exists, the strip
// stays as the bare status banner.
func applyDeliveryVerdict(strip deliveryStrip, verdicts []verdictCard) deliveryStrip {
	for _, v := range verdicts {
		if v.ContractName != "story_close" {
			continue
		}
		strip.Verdict = v.Verdict
		strip.Score = v.Score
		strip.UpdatedAt = v.CreatedAt
		switch v.Verdict {
		case "approved":
			strip.Resolution = "delivered"
		case "rejected":
			strip.Resolution = "failed"
		case "needs_changes", "amended":
			strip.Resolution = "partially_delivered"
		}
		break
	}
	return strip
}

// truncate clips s to maxRunes, appending an ellipsis when clipped.
// Operates on runes so multi-byte content doesn't slice mid-codepoint.
func truncate(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "…"
}
