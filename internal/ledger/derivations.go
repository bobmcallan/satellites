package ledger

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// KVScope is the visibility tier of a KV entry. Rows carry a
// `scope:<scope>` tag identifying the tier; rows that pre-date the
// scope-tag convention (e.g. existing key:workflow_spec rows) are
// treated as KVScopeProject — the legacy implicit shape.
type KVScope string

// KVScope enum. story_61abf197.
const (
	KVScopeSystem    KVScope = "system"
	KVScopeWorkspace KVScope = "workspace"
	KVScopeProject   KVScope = "project"
	KVScopeUser      KVScope = "user"
)

// KVRow is the projection-shape returned by KVProjection / KVProjectionScoped:
// the latest ledger row that carries a `key:<name>` tag plus the parsed
// value. Scope/UserID are populated from the row's `scope:<scope>` and
// `user:<id>` tags; legacy rows without a `scope:` tag report
// Scope=KVScopeProject.
type KVRow struct {
	Key       string
	Value     string
	Scope     KVScope
	UserID    string
	UpdatedAt time.Time
	UpdatedBy string
	EntryID   string
}

// CostSummary is the aggregate returned by CostRollup. Sums are taken
// over rows tagged kind:llm-usage; rows whose Structured payload doesn't
// parse contribute zero (caller can read SkippedRows to surface the
// signal).
type CostSummary struct {
	CostUSD      float64
	InputTokens  int64
	OutputTokens int64
	RowCount     int
	SkippedRows  int
}

// Tag prefixes for the KV ledger-row convention. A KV row carries one
// `key:<name>` tag, and (post-story_61abf197) optionally one
// `scope:<scope>` and one `user:<id>` tag. The existing ledger tag-array
// indexes (workspace_id, tags) cover lookup by both scope and key
// without requiring a new index.
const (
	kvKeyTagPrefix   = "key:"
	kvScopeTagPrefix = "scope:"
	kvUserTagPrefix  = "user:"
)

// KVTombstoneTag is the tag a KV row carries when it represents a
// delete. The append-only ledger has no Delete primitive, so KV deletes
// append a new row whose tags include `kind:tombstone`. KVProjectionScoped
// treats a key whose latest row is a tombstone as absent — the projection
// does not surface the prior value. story_3d392258.
const KVTombstoneTag = "kind:tombstone"

// KVProjectionOptions configures KVProjectionScoped. story_61abf197.
//
// Scope is required. The other identifier fields are required for
// scopes that name them: WorkspaceID for workspace/project/user;
// ProjectID for project; UserID for user. System scope ignores all
// identifier fields.
type KVProjectionOptions struct {
	Scope       KVScope
	WorkspaceID string
	ProjectID   string
	UserID      string
}

// KVProjection returns the latest Type=kv row per key inside projectID.
// Backwards-compatible wrapper that resolves to project-scope projection.
// Multiple rows for the same key shadow older versions; the newest by
// CreatedAt wins. Workspace-scoped per memberships.
func KVProjection(ctx context.Context, store Store, projectID string, memberships []string) (map[string]KVRow, error) {
	return KVProjectionScoped(ctx, store, KVProjectionOptions{
		Scope:     KVScopeProject,
		ProjectID: projectID,
	}, memberships)
}

// KVProjectionScoped returns the latest Type=kv row per key for the
// given scope-and-identifier tuple. Filters in-memory after a
// store.List call:
//
//   - System rows: scope tag = "scope:system", workspace_id = "" (the
//     system-actor convention used by the seed loader).
//   - Workspace rows: scope tag = "scope:workspace", workspace_id =
//     opts.WorkspaceID, project_id = "".
//   - Project rows: scope tag = "scope:project" OR no scope tag at all
//     (legacy default), workspace_id = opts.WorkspaceID (when supplied,
//     otherwise unconstrained), project_id = opts.ProjectID.
//   - User rows: scope tag = "scope:user", workspace_id =
//     opts.WorkspaceID, user tag = "user:<UserID>".
//
// Multiple rows for the same key shadow older versions per CreatedAt.
// Workspace-scoped per memberships (caller is responsible for including
// "" in memberships when querying system scope).
func KVProjectionScoped(ctx context.Context, store Store, opts KVProjectionOptions, memberships []string) (map[string]KVRow, error) {
	if opts.Scope == "" {
		return nil, fmt.Errorf("ledger: kv projection: scope is required")
	}
	listProjectID := ""
	if opts.Scope == KVScopeProject {
		listProjectID = opts.ProjectID
	}
	rows, err := store.List(ctx, listProjectID, ListOptions{Type: TypeKV, Limit: MaxListLimit}, memberships)
	if err != nil {
		return nil, fmt.Errorf("ledger: kv projection list: %w", err)
	}
	// Two-pass scan: track the latest row per key (regardless of whether
	// it's a tombstone), then exclude keys whose latest is a tombstone.
	type latest struct {
		row         LedgerEntry
		isTombstone bool
		scope       KVScope
		userID      string
	}
	winners := make(map[string]latest, len(rows))
	for _, e := range rows {
		key := extractKey(e.Tags)
		if key == "" {
			continue
		}
		rowScope := extractScope(e.Tags)
		rowUser := extractUserTag(e.Tags)
		if !matchesScope(opts, e, rowScope, rowUser) {
			continue
		}
		if cur, ok := winners[key]; ok && cur.row.CreatedAt.After(e.CreatedAt) {
			continue
		}
		winners[key] = latest{
			row:         e,
			isTombstone: containsTag(e.Tags, KVTombstoneTag),
			scope:       rowScope,
			userID:      rowUser,
		}
	}
	out := make(map[string]KVRow, len(winners))
	for key, w := range winners {
		if w.isTombstone {
			continue
		}
		out[key] = KVRow{
			Key:       key,
			Value:     w.row.Content,
			Scope:     w.scope,
			UserID:    w.userID,
			UpdatedAt: w.row.CreatedAt,
			UpdatedBy: w.row.CreatedBy,
			EntryID:   w.row.ID,
		}
	}
	return out, nil
}

// containsTag is a small helper for tombstone-detection on the
// projection path. The ledger's own anyTagMatch is unexported so we
// keep the trivial inline scan local to derivations.
func containsTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

// matchesScope filters a row against KVProjectionOptions. Legacy rows
// without a `scope:*` tag are treated as project-scope.
func matchesScope(opts KVProjectionOptions, e LedgerEntry, rowScope KVScope, rowUser string) bool {
	switch opts.Scope {
	case KVScopeSystem:
		return rowScope == KVScopeSystem && e.WorkspaceID == ""
	case KVScopeWorkspace:
		return rowScope == KVScopeWorkspace && e.WorkspaceID == opts.WorkspaceID && e.ProjectID == ""
	case KVScopeProject:
		// project_id filter is enforced by store.List via listProjectID;
		// require either an explicit scope:project tag or the legacy
		// no-scope shape.
		return rowScope == KVScopeProject
	case KVScopeUser:
		return rowScope == KVScopeUser && e.WorkspaceID == opts.WorkspaceID && rowUser == opts.UserID && rowUser != ""
	default:
		return false
	}
}

func extractKey(tags []string) string {
	for _, t := range tags {
		if strings.HasPrefix(t, kvKeyTagPrefix) {
			return strings.TrimPrefix(t, kvKeyTagPrefix)
		}
	}
	return ""
}

// extractScope returns the row's scope. Rows without a `scope:` tag
// default to KVScopeProject — the legacy implicit shape used by
// pre-story_61abf197 KV rows (e.g. key:workflow_spec).
func extractScope(tags []string) KVScope {
	for _, t := range tags {
		if strings.HasPrefix(t, kvScopeTagPrefix) {
			return KVScope(strings.TrimPrefix(t, kvScopeTagPrefix))
		}
	}
	return KVScopeProject
}

// extractUserTag returns the user_id encoded in a `user:<id>` tag, or
// "" when no such tag is present.
func extractUserTag(tags []string) string {
	for _, t := range tags {
		if strings.HasPrefix(t, kvUserTagPrefix) {
			return strings.TrimPrefix(t, kvUserTagPrefix)
		}
	}
	return ""
}

// StoryTimeline returns ledger rows scoped to storyID in CreatedAt ASC
// order — the natural shape for a portal story panel showing the audit
// trail. Workspace-scoped per memberships.
func StoryTimeline(ctx context.Context, store Store, storyID string, memberships []string) ([]LedgerEntry, error) {
	if storyID == "" {
		return nil, fmt.Errorf("ledger: timeline requires story id")
	}
	rows, err := store.List(ctx, "", ListOptions{StoryID: storyID, Limit: MaxListLimit, IncludeDerefd: true}, memberships)
	if err != nil {
		return nil, fmt.Errorf("ledger: story timeline list: %w", err)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].CreatedAt.Before(rows[j].CreatedAt) })
	return rows, nil
}

// CostRollup aggregates token + dollar usage across rows tagged
// `kind:llm-usage` inside projectID. Each row's Structured field is
// expected to be a JSON object with optional `cost_usd` (number),
// `input_tokens` (number), `output_tokens` (number); missing keys
// contribute zero. Rows whose Structured doesn't parse increment
// SkippedRows and otherwise contribute zero.
func CostRollup(ctx context.Context, store Store, projectID string, memberships []string) (CostSummary, error) {
	rows, err := store.List(ctx, projectID, ListOptions{Tags: []string{"kind:llm-usage"}, Limit: MaxListLimit, IncludeDerefd: true}, memberships)
	if err != nil {
		return CostSummary{}, fmt.Errorf("ledger: cost rollup list: %w", err)
	}
	summary := CostSummary{}
	for _, e := range rows {
		summary.RowCount++
		if len(e.Structured) == 0 {
			continue
		}
		var payload struct {
			CostUSD      float64 `json:"cost_usd"`
			InputTokens  int64   `json:"input_tokens"`
			OutputTokens int64   `json:"output_tokens"`
		}
		if err := json.Unmarshal(e.Structured, &payload); err != nil {
			summary.SkippedRows++
			continue
		}
		summary.CostUSD += payload.CostUSD
		summary.InputTokens += payload.InputTokens
		summary.OutputTokens += payload.OutputTokens
	}
	return summary, nil
}
