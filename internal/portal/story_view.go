// Story-view composite builder. Pulls the story-level panels described
// in docs/ui-design.md §2.2 — source docs / reviewer verdicts / repo
// provenance / ledger excerpts / activity / delivery strip — into one
// struct so the SSR template and the JSON composite endpoint render
// from the same shape.
//
// sty_c6d76a5b retired the contract-instance row type. The
// TaskChain slice on the composite is an always-empty placeholder:
// the canonical "what's happening on this story" view lives at
// /stories/{id}/walk, which renders the task chain via task_walk.
// Sty_509a46fa renamed the placeholder away from the dead noun.
package portal

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
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
// the reviewer service.
const verdictTagKind = "kind:verdict"

// distinctLedgerKinds names the kind:* tag values the story-detail
// timeline styles distinctly.
var distinctLedgerKinds = map[string]string{
	"kind:plan-amend":              "plan-amend",
	"kind:agent-compose":           "agent-compose",
	"kind:agent-archive":           "agent-archive",
	"kind:session-default-install": "session-default-install",
}

// storyComposite is the view-model for the story view.
type storyComposite struct {
	Story         storyRow           `json:"story"`
	SourceDocs    []sourceDocLink    `json:"source_documents"`
	TaskChain     []taskChainCard    `json:"task_chain"`
	Verdicts      []verdictCard      `json:"verdicts"`
	Commits       []commitCard       `json:"commits"`
	Excerpts      []ledgerExcerpt    `json:"ledger_excerpts"`
	Activity      []storyActivityRow `json:"activity"`
	ActivityKinds []string           `json:"activity_kinds"`
	Delivery      deliveryStrip      `json:"delivery"`
}

// sourceDocLink is the source-documents-panel view-model.
type sourceDocLink struct {
	Tag     string `json:"tag"`
	Path    string `json:"path"`
	Anchor  string `json:"anchor"`
	Display string `json:"display"`
}

// taskChainCard is an always-empty placeholder; the canonical
// task-chain view lives at /stories/{id}/walk.
type taskChainCard struct{}

// verdictCard is one reviewer-verdict row scoped to this story. The
// task_id tag (set by the reviewer service) carries the originating
// review task; ContractName comes from the `phase:<name>` tag.
type verdictCard struct {
	LedgerID     string `json:"ledger_id"`
	TaskID       string `json:"task_id,omitempty"`
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
	KindClass string   `json:"kind_class,omitempty"`
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
	docs document.Store,
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
		TaskChain:  []taskChainCard{},
		Delivery:   deliveryStrip{Status: s.Status, UpdatedAt: s.UpdatedAt.UTC().Format(time.RFC3339)},
	}
	_ = docs

	if ledgerStore != nil {
		c.Verdicts = verdictsForStory(ctx, ledgerStore, s.ProjectID, storyID, memberships)
		c.Commits = commitsForStory(ctx, ledgerStore, s.ProjectID, storyID, memberships)
		c.Excerpts = excerptsForStory(ctx, ledgerStore, s.ProjectID, storyID, memberships)
		c.ActivityKinds = resolveStoryActivityKinds(ctx, ledgerStore, s.WorkspaceID, s.ProjectID, memberships)
		c.Activity = buildStoryActivity(ctx, ledgerStore, s.ProjectID, storyID, c.ActivityKinds, memberships)
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

// verdictsForStory pulls the kind:verdict ledger rows for the story,
// newest-first.
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
		for _, t := range r.Tags {
			switch {
			case strings.HasPrefix(t, "phase:"):
				card.ContractName = strings.TrimPrefix(t, "phase:")
			case strings.HasPrefix(t, "task_id:"):
				card.TaskID = strings.TrimPrefix(t, "task_id:")
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
// the story (any tag).
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
			KindClass: ledgerKindClass(r.Tags),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out
}

// ledgerKindClass returns the CSS suffix for the distinct lifecycle
// kinds.
func ledgerKindClass(tags []string) string {
	for _, t := range tags {
		if cls, ok := distinctLedgerKinds[t]; ok {
			return cls
		}
	}
	return ""
}

// applyDeliveryVerdict folds the most recent story_close verdict into
// the delivery strip.
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
