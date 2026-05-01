package changelog

import (
	"context"
	"fmt"
	"time"

	"github.com/surrealdb/surrealdb.go"
	surrealmodels "github.com/surrealdb/surrealdb.go/pkg/models"
)

// SurrealStore is a SurrealDB-backed Store for the `changelog` table.
// Idempotent table-define on construction (sty_12af0bdc).
type SurrealStore struct {
	db *surrealdb.DB
}

// NewSurrealStore wraps db as a Store and ensures the table exists.
func NewSurrealStore(db *surrealdb.DB) *SurrealStore {
	s := &SurrealStore{db: db}
	_, _ = surrealdb.Query[any](context.Background(), db, "DEFINE TABLE IF NOT EXISTS changelog SCHEMALESS", nil)
	return s
}

// selectCols preserves the string id (matches the pattern used by
// internal/story/surreal.go).
const selectCols = "meta::id(id) AS id, workspace_id, project_id, service, version_from, version_to, content, effective_date, created_by, created_at, updated_at"

func (s *SurrealStore) Create(ctx context.Context, c Changelog, now time.Time) (Changelog, error) {
	c.ID = NewID()
	c.CreatedAt = now
	c.UpdatedAt = now
	if err := s.write(ctx, c); err != nil {
		return Changelog{}, err
	}
	return c, nil
}

func (s *SurrealStore) GetByID(ctx context.Context, id string, memberships []string) (Changelog, error) {
	where := "id = $rid"
	vars := map[string]any{"rid": surrealmodels.NewRecordID("changelog", id)}
	if memberships != nil {
		where += " AND workspace_id IN $memberships"
		vars["memberships"] = memberships
	}
	sql := fmt.Sprintf("SELECT %s FROM changelog WHERE %s LIMIT 1", selectCols, where)
	results, err := surrealdb.Query[[]Changelog](ctx, s.db, sql, vars)
	if err != nil {
		return Changelog{}, fmt.Errorf("changelog: get: %w", err)
	}
	if results == nil || len(*results) == 0 || len((*results)[0].Result) == 0 {
		return Changelog{}, ErrNotFound
	}
	return (*results)[0].Result[0], nil
}

func (s *SurrealStore) List(ctx context.Context, opts ListOptions, memberships []string) ([]Changelog, error) {
	opts = opts.normalised()
	where := "1=1"
	vars := map[string]any{"limit": opts.Limit}
	if opts.ProjectID != "" {
		where += " AND project_id = $project_id"
		vars["project_id"] = opts.ProjectID
	}
	if opts.Service != "" {
		where += " AND service = $service"
		vars["service"] = opts.Service
	}
	if memberships != nil {
		where += " AND workspace_id IN $memberships"
		vars["memberships"] = memberships
	}
	sql := fmt.Sprintf(
		"SELECT %s FROM changelog WHERE %s ORDER BY created_at DESC, id DESC LIMIT $limit",
		selectCols, where,
	)
	results, err := surrealdb.Query[[]Changelog](ctx, s.db, sql, vars)
	if err != nil {
		return nil, fmt.Errorf("changelog: list: %w", err)
	}
	if results == nil || len(*results) == 0 {
		return []Changelog{}, nil
	}
	return (*results)[0].Result, nil
}

func (s *SurrealStore) Update(ctx context.Context, id string, fields UpdateFields, now time.Time, memberships []string) (Changelog, error) {
	current, err := s.GetByID(ctx, id, memberships)
	if err != nil {
		return Changelog{}, err
	}
	applyUpdate(&current, fields)
	current.UpdatedAt = now
	if err := s.write(ctx, current); err != nil {
		return Changelog{}, err
	}
	return current, nil
}

func (s *SurrealStore) Delete(ctx context.Context, id string, memberships []string) error {
	if _, err := s.GetByID(ctx, id, memberships); err != nil {
		return err
	}
	sql := "DELETE $rid"
	vars := map[string]any{"rid": surrealmodels.NewRecordID("changelog", id)}
	if _, err := surrealdb.Query[any](ctx, s.db, sql, vars); err != nil {
		return fmt.Errorf("changelog: delete: %w", err)
	}
	return nil
}

func (s *SurrealStore) write(ctx context.Context, c Changelog) error {
	sql := "UPSERT $rid CONTENT $doc"
	vars := map[string]any{
		"rid": surrealmodels.NewRecordID("changelog", c.ID),
		"doc": c,
	}
	if _, err := surrealdb.Query[[]Changelog](ctx, s.db, sql, vars); err != nil {
		return fmt.Errorf("changelog: upsert: %w", err)
	}
	return nil
}

// Compile-time assertion.
var _ Store = (*SurrealStore)(nil)
