// Story-view composite builder for slice 11.1 (story_3b450d9e). Pulls
// the five panels described in docs/ui-design.md §2.2 — scope/source
// docs / contract-instance timeline / reviewer verdicts / repo
// provenance — into one struct so the SSR template and the JSON
// composite endpoint render from the same shape.
//
// Story_7b77ffb0 (portal UI for role-based execution) extends ciCard
// with parent_invocation_id depth, ac_scope label, allocated agent_id +
// resolved name, per-AC iteration counter, and tags ledger excerpts
// with kind classes so the timeline can style plan-amend /
// agent-compose / agent-archive / session-default-install rows
// distinctly.
package portal

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/contract"
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
// internal/mcpserver/close_handlers.go writeVerdictRow.
const verdictTagKind = "kind:verdict"

// distinctLedgerKinds names the kind:* tag values that the
// story-detail timeline styles distinctly. Story_7b77ffb0 AC8: the
// timeline must visually separate these rows from generic
// kind:close-request / kind:action-claim / kind:plan rows.
var distinctLedgerKinds = map[string]string{
	"kind:plan-amend":              "plan-amend",
	"kind:agent-compose":           "agent-compose",
	"kind:agent-archive":           "agent-archive",
	"kind:session-default-install": "session-default-install",
}

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
	ID                 string `json:"id"`
	ContractName       string `json:"contract_name"`
	Sequence           int    `json:"sequence"`
	Status             string `json:"status"`
	ClaimedAt          string `json:"claimed_at,omitempty"`
	ClosedAt           string `json:"closed_at,omitempty"`
	PlanLedgerID       string `json:"plan_ledger_id,omitempty"`
	CloseLedgerID      string `json:"close_ledger_id,omitempty"`
	ParentInvocationID string `json:"parent_invocation_id,omitempty"`
	Depth              int    `json:"depth"`
	ACScope            []int  `json:"ac_scope,omitempty"`
	ACScopeLabel       string `json:"ac_scope_label,omitempty"`
	AgentID            string `json:"agent_id,omitempty"`
	AgentName          string `json:"agent_name,omitempty"`
	AgentHref          string `json:"agent_href,omitempty"`
	Iteration          int    `json:"iteration,omitempty"`
	IterationCap       int    `json:"iteration_cap,omitempty"`
	IterationWarn      bool   `json:"iteration_warn,omitempty"`
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
	contracts contract.Store,
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
		Delivery:   deliveryStrip{Status: s.Status, UpdatedAt: s.UpdatedAt.UTC().Format(time.RFC3339)},
	}

	if contracts != nil {
		cis, err := contracts.List(ctx, storyID, memberships)
		if err == nil {
			c.CIs = ciCardsFor(ctx, cis, docs, memberships)
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
// view-model. CIs are reordered so that children render directly after
// their parent (story_d5d88a64 tree walk); each row carries a Depth
// computed from the parent chain. Story_7b77ffb0 also stamps the
// ac_scope chip label, the allocated agent_id + name, and the per-AC
// iteration counter.
func ciCardsFor(ctx context.Context, cis []contract.ContractInstance, docs document.Store, memberships []string) []ciCard {
	if len(cis) == 0 {
		return []ciCard{}
	}
	ordered := contract.TreeWalk(cis)
	depthByID := computeCIDepths(ordered)
	cap := contract.MaxACIterations()

	// Resolve agent docs once per unique AgentID — the timeline render
	// links each CI to its allocated agent's name.
	agentNames := make(map[string]string)
	for _, ci := range ordered {
		if ci.AgentID == "" || docs == nil {
			continue
		}
		if _, ok := agentNames[ci.AgentID]; ok {
			continue
		}
		d, err := docs.GetByID(ctx, ci.AgentID, memberships)
		if err == nil {
			agentNames[ci.AgentID] = d.Name
		} else {
			agentNames[ci.AgentID] = ""
		}
	}

	out := make([]ciCard, 0, len(ordered))
	for _, ci := range ordered {
		card := ciCard{
			ID:                 ci.ID,
			ContractName:       ci.ContractName,
			Sequence:           ci.Sequence,
			Status:             ci.Status,
			PlanLedgerID:       ci.PlanLedgerID,
			CloseLedgerID:      ci.CloseLedgerID,
			ParentInvocationID: ci.ParentInvocationID,
			Depth:              depthByID[ci.ID],
			ACScope:            append([]int(nil), ci.ACScope...),
			ACScopeLabel:       acScopeLabel(ci.ACScope),
			AgentID:            ci.AgentID,
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
		if card.AgentID != "" {
			card.AgentName = agentNames[card.AgentID]
			card.AgentHref = "/documents/" + card.AgentID
		}
		// Per-AC iteration: use the highest iteration index across this
		// CI's ACScope so a multi-AC CI surfaces its riskiest counter.
		if iter, _, warn := iterationFor(ci, cis, cap); iter > 0 {
			card.Iteration = iter
			card.IterationCap = cap
			card.IterationWarn = warn
		}
		out = append(out, card)
	}
	return out
}

// computeCIDepths walks the tree-walked CI slice and returns the depth
// of each CI relative to its root. CIs without ParentInvocationID, or
// whose parent isn't in the slice, are roots (depth 0).
func computeCIDepths(ordered []contract.ContractInstance) map[string]int {
	idx := make(map[string]int, len(ordered))
	for i, c := range ordered {
		idx[c.ID] = i
	}
	depth := make(map[string]int, len(ordered))
	for _, c := range ordered {
		if c.ParentInvocationID == "" {
			depth[c.ID] = 0
			continue
		}
		if _, ok := idx[c.ParentInvocationID]; !ok {
			depth[c.ID] = 0
			continue
		}
		depth[c.ID] = depth[c.ParentInvocationID] + 1
	}
	return depth
}

// acScopeLabel formats an ACScope slice for the CI chip. Empty scope
// renders "AC 1..N" elsewhere; the helper itself returns "" for empty
// so the template can decide. A contiguous range "AC 1..5" is preferred
// when the indices form a contiguous run; otherwise a comma list "AC
// 2, 4".
func acScopeLabel(scope []int) string {
	if len(scope) == 0 {
		return ""
	}
	sorted := append([]int(nil), scope...)
	sort.Ints(sorted)
	if len(sorted) == 1 {
		return fmt.Sprintf("AC %d", sorted[0])
	}
	contiguous := true
	for i := 1; i < len(sorted); i++ {
		if sorted[i] != sorted[i-1]+1 {
			contiguous = false
			break
		}
	}
	if contiguous {
		return fmt.Sprintf("AC %d..%d", sorted[0], sorted[len(sorted)-1])
	}
	parts := make([]string, len(sorted))
	for i, v := range sorted {
		parts[i] = fmt.Sprint(v)
	}
	return "AC " + strings.Join(parts, ", ")
}

// iterationFor computes the per-AC iteration counter for ci. When ci's
// ACScope is empty the iteration is 0 (no per-AC re-scope happened).
// Otherwise it returns the maximum iteration count across ci's ACs,
// computed against all CIs on the story. Returns warn=true when the
// iteration count exceeds cap/2 — the early-warning threshold per
// AC7.
func iterationFor(ci contract.ContractInstance, all []contract.ContractInstance, cap int) (int, int, bool) {
	if len(ci.ACScope) == 0 {
		return 0, cap, false
	}
	maxIter := 0
	for _, ac := range ci.ACScope {
		// Count CIs created on or before ci that include this ac index;
		// the iteration number of ci itself is its position in that
		// stream. The substrate uses created_at ordering so a stable
		// "third re-scope of AC 2" surfaces here.
		seen := 0
		for _, other := range all {
			if other.CreatedAt.After(ci.CreatedAt) {
				continue
			}
			for _, oa := range other.ACScope {
				if oa == ac {
					seen++
					break
				}
			}
		}
		if seen > maxIter {
			maxIter = seen
		}
	}
	if cap <= 0 {
		cap = contract.DefaultMaxACIterations
	}
	half := cap / 2
	warn := maxIter > half
	return maxIter, cap, warn
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
// Story_7b77ffb0 stamps a KindClass on each row when its tags match
// one of the distinct lifecycle kinds (plan-amend / agent-compose /
// agent-archive / session-default-install) so the template can style
// those rows distinctly.
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
// kinds (story_7b77ffb0 AC8). Empty string when no recognised tag is
// present — the template falls back to its default styling.
func ledgerKindClass(tags []string) string {
	for _, t := range tags {
		if cls, ok := distinctLedgerKinds[t]; ok {
			return cls
		}
	}
	return ""
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
