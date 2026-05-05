// Per-story activity-log composite for sty_e55f335e. The activity panel
// renders a curated subset of ledger rows scoped to one story — the
// substrate-internal lifecycle events (plan, agent-compose,
// action-claim, close-request, evidence, artifact, verdict, review
// q/a) — in time order. Distinct from the ledger-excerpts panel, which
// shows every row regardless of kind.
//
// Backfill is served by GET /api/stories/{story_id}/activity. Live
// updates ride the existing workspace websocket via the
// `story.activity.append` event kind that internal/ledger/emit.go
// publishes alongside the generic `ledger.append` whenever a row's
// kind tag is in the activity set.
package portal

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/ledger"
)

// DefaultStoryActivityKinds enumerates the kind:* tag values the
// activity panel renders by default. Operators may override this set
// per-project with a ledger KV write at key
// `story_activity.kinds` (scope=project) carrying a JSON array of
// kind tag strings — see resolveStoryActivityKinds.
//
// Order is significant for documentation: panels and dashboards may
// render the kinds in declaration order. The actual filter is
// set-membership.
var DefaultStoryActivityKinds = []string{
	"kind:workflow-claim",
	"kind:plan",
	"kind:plan-approved",
	"kind:plan-amend",
	"kind:agent-compose",
	"kind:action-claim",
	"kind:close-request",
	"kind:evidence",
	"kind:artifact",
	"kind:review-question",
	"kind:review-response",
	"kind:verdict",
}

// StoryActivityKVKey is the project-scope KV key operators set to
// override the default activity-kind list. Value is a JSON array of
// kind:* tag strings; missing or malformed values fall back to
// DefaultStoryActivityKinds.
const StoryActivityKVKey = "story_activity.kinds"

// activityLimit caps the per-story activity backfill. Live appends
// stream beyond the cap; the cap exists so a story with thousands of
// rows doesn't ship the whole history on first load.
const activityLimit = 500

// activityContentTrim caps the one-line summary length. Rows whose
// content exceeds this are truncated with an ellipsis; the panel
// renders the full row content via the linked ledger detail.
const activityContentTrim = 240

// storyActivityRow is one row in the activity panel. ID is the
// underlying ledger entry id (`ldg_<8hex>`). Kind is the matched
// activity-kind tag (e.g. `kind:plan`). Phase is the contract slot
// derived from a `phase:<contract_name>` tag, empty when none.
// Summary is a one-line human snippet derived from the row's
// content. CreatedAt is RFC3339 UTC.
type storyActivityRow struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	KindClass    string   `json:"kind_class,omitempty"`
	Phase        string   `json:"contract_slot,omitempty"`
	Type         string   `json:"type"`
	Tags         []string `json:"tags"`
	Summary      string   `json:"summary"`
	StructuredOK bool     `json:"-"`
	CreatedAt    string   `json:"created_at"`
}

// storyActivityComposite is the JSON shape served by
// /api/stories/{story_id}/activity and consumed by the panel's first-
// load fetch. Kinds carries the resolved filter set so the client can
// surface "showing N kinds" without a second round-trip.
type storyActivityComposite struct {
	StoryID string             `json:"story_id"`
	Kinds   []string           `json:"kinds"`
	Rows    []storyActivityRow `json:"rows"`
}

// buildStoryActivity assembles the activity rows for storyID, ordered
// oldest-first (created_at ASC). Filters server-side by kind tag —
// rows whose tags don't intersect kinds are dropped before return.
// memberships scopes the query to the caller's workspaces; cross-
// workspace stories return an empty composite (caller should 404).
func buildStoryActivity(
	ctx context.Context,
	store ledger.Store,
	projectID, storyID string,
	kinds []string,
	memberships []string,
) []storyActivityRow {
	if store == nil || storyID == "" {
		return []storyActivityRow{}
	}
	rows, err := store.List(ctx, projectID, ledger.ListOptions{
		StoryID:       storyID,
		IncludeDerefd: true,
		Limit:         activityLimit,
	}, memberships)
	if err != nil {
		return []storyActivityRow{}
	}
	kindSet := make(map[string]struct{}, len(kinds))
	for _, k := range kinds {
		kindSet[k] = struct{}{}
	}
	out := make([]storyActivityRow, 0, len(rows))
	for _, r := range rows {
		kind, matched := matchActivityKind(r.Tags, kindSet)
		if !matched {
			continue
		}
		row := storyActivityRow{
			ID:        r.ID,
			Kind:      kind,
			KindClass: kindCSSClass(kind),
			Phase:     extractTagValue(r.Tags, "phase:"),
			Type:      r.Type,
			Tags:      append([]string(nil), r.Tags...),
			Summary:   truncate(r.Content, activityContentTrim),
			CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339),
		}
		out = append(out, row)
	}
	// Oldest-first so the panel reads as a timeline.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// matchActivityKind reports whether tags carry a kind:* tag whose value
// is in the supplied set. Returns the matching tag string on success.
// First match wins so a row tagged with multiple kinds (rare but legal)
// surfaces under the kind that appears first.
func matchActivityKind(tags []string, set map[string]struct{}) (string, bool) {
	for _, t := range tags {
		if !strings.HasPrefix(t, "kind:") {
			continue
		}
		if _, ok := set[t]; ok {
			return t, true
		}
	}
	return "", false
}

// extractTagValue scans tags for the first prefixed entry and returns
// its suffix (the value after the colon). Empty string when no match.
func extractTagValue(tags []string, prefix string) string {
	for _, t := range tags {
		if strings.HasPrefix(t, prefix) {
			return strings.TrimPrefix(t, prefix)
		}
	}
	return ""
}

// kindCSSClass maps an activity-kind tag to a stable CSS-friendly slug
// the template uses for per-row styling. e.g. "kind:plan" → "plan".
// Empty input → empty output.
func kindCSSClass(kind string) string {
	if kind == "" {
		return ""
	}
	return strings.TrimPrefix(kind, "kind:")
}

// resolveStoryActivityKinds returns the kind set the activity panel
// renders for projectID. When a project-scope KV row at
// StoryActivityKVKey carries a JSON array of kind tag strings, those
// override the default. Malformed, empty, or absent values fall back
// to DefaultStoryActivityKinds. Workspace memberships scope the read
// identically to the existing KV chain.
func resolveStoryActivityKinds(
	ctx context.Context,
	store ledger.Store,
	workspaceID, projectID string,
	memberships []string,
) []string {
	if store == nil || projectID == "" {
		return DefaultStoryActivityKinds
	}
	row, ok, err := ledger.KVResolveScoped(ctx, store, StoryActivityKVKey, ledger.KVResolveOptions{
		WorkspaceID: workspaceID,
		ProjectID:   projectID,
	}, memberships)
	if err != nil || !ok {
		return DefaultStoryActivityKinds
	}
	parsed := parseActivityKindsJSON(row.Value)
	if len(parsed) == 0 {
		return DefaultStoryActivityKinds
	}
	return parsed
}

// parseActivityKindsJSON decodes the KV value at StoryActivityKVKey
// into the kind slice. Accepts either a JSON array of strings or a
// comma-separated string for operator convenience. Empty / malformed
// → empty slice (caller falls back to default).
func parseActivityKindsJSON(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if strings.HasPrefix(value, "[") {
		var arr []string
		if err := json.Unmarshal([]byte(value), &arr); err != nil {
			return nil
		}
		return normaliseKinds(arr)
	}
	parts := strings.Split(value, ",")
	return normaliseKinds(parts)
}

// normaliseKinds trims and de-empties; non-`kind:` prefixed entries are
// rewritten with the prefix so operators can write `plan` or
// `kind:plan` interchangeably.
func normaliseKinds(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if !strings.HasPrefix(s, "kind:") {
			s = "kind:" + s
		}
		out = append(out, s)
	}
	return out
}

// IsStoryActivityKindTagged returns true when tags carry a kind:* tag
// whose value is in the supplied activity-kind set. Exported because
// internal/ledger/emit.go calls it on every append to decide whether
// to publish the `story.activity.append` event alongside the generic
// `ledger.append`. A nil/empty kinds slice falls back to the default.
func IsStoryActivityKindTagged(tags, kinds []string) bool {
	if len(tags) == 0 {
		return false
	}
	if len(kinds) == 0 {
		kinds = DefaultStoryActivityKinds
	}
	set := make(map[string]struct{}, len(kinds))
	for _, k := range kinds {
		set[k] = struct{}{}
	}
	_, ok := matchActivityKind(tags, set)
	return ok
}
