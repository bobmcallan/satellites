package ledger

import (
	"context"
	"fmt"
	"time"

	"github.com/surrealdb/surrealdb.go"
	surrealmodels "github.com/surrealdb/surrealdb.go/pkg/models"
)

// SurrealStore is a SurrealDB-backed Store. The caller must have already
// authenticated and selected ns/db on the supplied *surrealdb.DB.
type SurrealStore struct {
	db *surrealdb.DB
}

// NewSurrealStore wraps db as a Store. Defines the `ledger` table
// schemaless so first-time SELECTs don't error on a missing table; also
// declares the §6 access indexes — idempotent under DEFINE INDEX IF NOT
// EXISTS.
func NewSurrealStore(db *surrealdb.DB) *SurrealStore {
	s := &SurrealStore{db: db}
	ctx := context.Background()
	_, _ = surrealdb.Query[any](ctx, db, "DEFINE TABLE IF NOT EXISTS ledger SCHEMALESS", nil)
	_, _ = surrealdb.Query[any](ctx, db, "DEFINE INDEX IF NOT EXISTS ledger_ws_story_created ON ledger FIELDS workspace_id, story_id, created_at", nil)
	_, _ = surrealdb.Query[any](ctx, db, "DEFINE INDEX IF NOT EXISTS ledger_ws_contract ON ledger FIELDS workspace_id, contract_id", nil)
	_, _ = surrealdb.Query[any](ctx, db, "DEFINE INDEX IF NOT EXISTS ledger_ws_tags ON ledger FIELDS workspace_id, tags", nil)
	return s
}

// selectCols preserves the string form of id (see internal/project/surreal.go).
const selectCols = "meta::id(id) AS id, workspace_id, project_id, story_id, contract_id, type, tags, content, structured, durability, expires_at, source_type, sensitive, status, created_at, created_by"

// Append implements Store for SurrealStore.
func (s *SurrealStore) Append(ctx context.Context, entry LedgerEntry, now time.Time) (LedgerEntry, error) {
	applyDefaults(&entry)
	if err := entry.Validate(); err != nil {
		return LedgerEntry{}, err
	}
	entry.ID = NewID()
	entry.CreatedAt = now
	sql := "UPSERT $rid CONTENT $doc"
	vars := map[string]any{
		"rid": surrealmodels.NewRecordID("ledger", entry.ID),
		"doc": entry,
	}
	if _, err := surrealdb.Query[[]LedgerEntry](ctx, s.db, sql, vars); err != nil {
		return LedgerEntry{}, fmt.Errorf("ledger: append: %w", err)
	}
	return entry, nil
}

// List implements Store for SurrealStore. Newest-first, limit clamped.
// Default behaviour excludes dereferenced rows; the slice 7.2 verb layer
// adds a status filter that lets callers opt in.
func (s *SurrealStore) List(ctx context.Context, projectID string, opts ListOptions, memberships []string) ([]LedgerEntry, error) {
	opts = opts.normalised()
	if memberships != nil && len(memberships) == 0 {
		return []LedgerEntry{}, nil
	}
	conds := []string{"project_id = $project", "(status IS NONE OR status != 'dereferenced')"}
	vars := map[string]any{"project": projectID, "lim": opts.Limit}
	if memberships != nil {
		conds = append(conds, "workspace_id IN $memberships")
		vars["memberships"] = memberships
	}
	if opts.Type != "" {
		conds = append(conds, "type = $type")
		vars["type"] = opts.Type
	}
	where := conds[0]
	for i := 1; i < len(conds); i++ {
		where += " AND " + conds[i]
	}
	sql := fmt.Sprintf("SELECT %s FROM ledger WHERE %s ORDER BY created_at DESC LIMIT $lim", selectCols, where)
	results, err := surrealdb.Query[[]LedgerEntry](ctx, s.db, sql, vars)
	if err != nil {
		return nil, fmt.Errorf("ledger: list: %w", err)
	}
	if results == nil || len(*results) == 0 {
		return []LedgerEntry{}, nil
	}
	return (*results)[0].Result, nil
}

// BackfillWorkspaceID implements Store for SurrealStore.
func (s *SurrealStore) BackfillWorkspaceID(ctx context.Context, projectID, workspaceID string) (int, error) {
	sql := "UPDATE ledger SET workspace_id = $ws WHERE project_id = $project AND (workspace_id IS NONE OR workspace_id = '') RETURN AFTER"
	vars := map[string]any{"ws": workspaceID, "project": projectID}
	results, err := surrealdb.Query[[]LedgerEntry](ctx, s.db, sql, vars)
	if err != nil {
		return 0, fmt.Errorf("ledger: backfill workspace_id: %w", err)
	}
	if results == nil || len(*results) == 0 {
		return 0, nil
	}
	return len((*results)[0].Result), nil
}

// MigrateLegacyRows stamps the v4 enum + naming on rows that pre-date the
// schema reshape (story_368cd70f). Idempotent on every boot. Once every
// row has a non-empty `created_by`, the legacy `actor` field is dropped.
func (s *SurrealStore) MigrateLegacyRows(ctx context.Context, now time.Time) (int, error) {
	stamps := []struct {
		label string
		sql   string
	}{
		{"created_by=actor", "UPDATE ledger SET created_by = actor WHERE (created_by IS NONE OR created_by = '') AND actor IS NOT NONE RETURN AFTER"},
		{"durability=durable", "UPDATE ledger SET durability = 'durable' WHERE durability IS NONE OR durability = '' RETURN AFTER"},
		{"source_type=agent", "UPDATE ledger SET source_type = 'agent' WHERE source_type IS NONE OR source_type = '' RETURN AFTER"},
		{"status=active", "UPDATE ledger SET status = 'active' WHERE status IS NONE OR status = '' RETURN AFTER"},
	}
	stamped := 0
	for _, q := range stamps {
		results, err := surrealdb.Query[[]LedgerEntry](ctx, s.db, q.sql, nil)
		if err != nil {
			return stamped, fmt.Errorf("ledger: migrate %s: %w", q.label, err)
		}
		if results != nil && len(*results) > 0 {
			stamped += len((*results)[0].Result)
		}
	}
	type cnt struct {
		N int `json:"n"`
	}
	countSQL := "SELECT count() AS n FROM ledger WHERE actor IS NOT NONE AND actor != '' GROUP ALL"
	cres, err := surrealdb.Query[[]cnt](ctx, s.db, countSQL, nil)
	if err != nil {
		return stamped, nil
	}
	remaining := 0
	if cres != nil && len(*cres) > 0 && len((*cres)[0].Result) > 0 {
		remaining = (*cres)[0].Result[0].N
	}
	if remaining == 0 {
		_, _ = surrealdb.Query[any](ctx, s.db, "REMOVE FIELD actor ON ledger", nil)
	}
	return stamped, nil
}

// Compile-time assertion that SurrealStore satisfies Store.
var _ Store = (*SurrealStore)(nil)
