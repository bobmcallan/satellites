package project

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/surrealdb/surrealdb.go"
	surrealmodels "github.com/surrealdb/surrealdb.go/pkg/models"
)

// SurrealStore is a SurrealDB-backed Store. The caller must have already
// authenticated and selected ns/db on the supplied *surrealdb.DB (see
// internal/db.Connect).
type SurrealStore struct {
	db *surrealdb.DB
}

// NewSurrealStore wraps db as a Store. Defines the `projects` table
// schemaless so first-time SELECTs don't error on a missing table.
func NewSurrealStore(db *surrealdb.DB) *SurrealStore {
	s := &SurrealStore{db: db}
	_, _ = surrealdb.Query[any](context.Background(), db, "DEFINE TABLE IF NOT EXISTS projects SCHEMALESS", nil)
	return s
}

// Create implements Store for SurrealStore.
func (s *SurrealStore) Create(ctx context.Context, ownerUserID, workspaceID, name string, now time.Time) (Project, error) {
	return s.CreateWithRemote(ctx, ownerUserID, workspaceID, name, "", now)
}

// CreateWithRemote implements Store for SurrealStore. Uniqueness on
// (workspace_id, git_remote) is enforced by checking before write —
// SurrealDB schemaless tables don't carry index constraints we can rely
// on across older rows. The window between check and write is small but
// non-zero; for stronger guarantees, install a UNIQUE index on the
// projects table once SurrealDB schema migrations are wired.
func (s *SurrealStore) CreateWithRemote(ctx context.Context, ownerUserID, workspaceID, name, gitRemote string, now time.Time) (Project, error) {
	if gitRemote != "" {
		if existing, err := s.GetByGitRemote(ctx, workspaceID, gitRemote); err == nil {
			_ = existing
			return Project{}, ErrDuplicateGitRemote
		} else if !errors.Is(err, ErrNotFound) {
			return Project{}, err
		}
	}
	p := Project{
		ID:          NewID(),
		WorkspaceID: workspaceID,
		Name:        name,
		GitRemote:   gitRemote,
		OwnerUserID: ownerUserID,
		Status:      StatusActive,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.write(ctx, p); err != nil {
		return Project{}, err
	}
	return p, nil
}

// GetByGitRemote implements Store for SurrealStore.
func (s *SurrealStore) GetByGitRemote(ctx context.Context, workspaceID, gitRemote string) (Project, error) {
	if gitRemote == "" {
		return Project{}, ErrNotFound
	}
	sql := fmt.Sprintf("SELECT %s FROM projects WHERE workspace_id = $ws AND git_remote = $rem AND status = $st LIMIT 1", selectCols)
	vars := map[string]any{"ws": workspaceID, "rem": gitRemote, "st": StatusActive}
	results, err := surrealdb.Query[[]Project](ctx, s.db, sql, vars)
	if err != nil {
		return Project{}, fmt.Errorf("project: select by git_remote: %w", err)
	}
	if results == nil || len(*results) == 0 || len((*results)[0].Result) == 0 {
		return Project{}, ErrNotFound
	}
	return (*results)[0].Result[0], nil
}

// selectCols expands to a SELECT that preserves the string form of id.
// SurrealDB otherwise returns id as a RecordID object, which JSON-unmarshals
// as empty into `ID string`. `meta::id(id) AS id` returns just the id portion
// (e.g. "proj_xxx") without the table prefix.
const selectCols = "meta::id(id) AS id, workspace_id, name, git_remote, owner_user_id, status, created_at, updated_at"

// GetByID implements Store for SurrealStore. Membership filter matches
// memorystore semantics: nil = no scoping, empty = deny-all, non-empty =
// `workspace_id IN memberships`.
func (s *SurrealStore) GetByID(ctx context.Context, id string, memberships []string) (Project, error) {
	if memberships != nil && len(memberships) == 0 {
		return Project{}, ErrNotFound
	}
	where := "id = $rid"
	vars := map[string]any{"rid": surrealmodels.NewRecordID("projects", id)}
	if memberships != nil {
		where += " AND workspace_id IN $memberships"
		vars["memberships"] = memberships
	}
	sql := fmt.Sprintf("SELECT %s FROM projects WHERE %s LIMIT 1", selectCols, where)
	results, err := surrealdb.Query[[]Project](ctx, s.db, sql, vars)
	if err != nil {
		return Project{}, fmt.Errorf("project: select by id: %w", err)
	}
	if results == nil || len(*results) == 0 || len((*results)[0].Result) == 0 {
		return Project{}, ErrNotFound
	}
	return (*results)[0].Result[0], nil
}

// ListByOwner implements Store for SurrealStore. Newest-first.
func (s *SurrealStore) ListByOwner(ctx context.Context, ownerUserID string, memberships []string) ([]Project, error) {
	if memberships != nil && len(memberships) == 0 {
		return []Project{}, nil
	}
	where := "owner_user_id = $owner"
	vars := map[string]any{"owner": ownerUserID}
	if memberships != nil {
		where += " AND workspace_id IN $memberships"
		vars["memberships"] = memberships
	}
	sql := fmt.Sprintf("SELECT %s FROM projects WHERE %s ORDER BY created_at DESC", selectCols, where)
	results, err := surrealdb.Query[[]Project](ctx, s.db, sql, vars)
	if err != nil {
		return nil, fmt.Errorf("project: list by owner: %w", err)
	}
	if results == nil || len(*results) == 0 {
		return []Project{}, nil
	}
	return (*results)[0].Result, nil
}

// UpdateName implements Store for SurrealStore.
func (s *SurrealStore) UpdateName(ctx context.Context, id, name string, now time.Time) (Project, error) {
	existing, err := s.GetByID(ctx, id, nil)
	if err != nil {
		return Project{}, err
	}
	existing.Name = name
	existing.UpdatedAt = now
	if err := s.write(ctx, existing); err != nil {
		return Project{}, err
	}
	return existing, nil
}

// SetGitRemote implements Store for SurrealStore.
func (s *SurrealStore) SetGitRemote(ctx context.Context, id, gitRemote string, now time.Time) (Project, error) {
	existing, err := s.GetByID(ctx, id, nil)
	if err != nil {
		return Project{}, err
	}
	if gitRemote != "" && gitRemote != existing.GitRemote {
		if dup, dupErr := s.GetByGitRemote(ctx, existing.WorkspaceID, gitRemote); dupErr == nil && dup.ID != id {
			return Project{}, ErrDuplicateGitRemote
		} else if dupErr != nil && !errors.Is(dupErr, ErrNotFound) {
			return Project{}, dupErr
		}
	}
	existing.GitRemote = gitRemote
	existing.UpdatedAt = now
	if err := s.write(ctx, existing); err != nil {
		return Project{}, err
	}
	return existing, nil
}

// SetStatus implements Store for SurrealStore.
func (s *SurrealStore) SetStatus(ctx context.Context, id, status string, now time.Time) (Project, error) {
	existing, err := s.GetByID(ctx, id, nil)
	if err != nil {
		return Project{}, err
	}
	existing.Status = status
	existing.UpdatedAt = now
	if err := s.write(ctx, existing); err != nil {
		return Project{}, err
	}
	return existing, nil
}

func (s *SurrealStore) write(ctx context.Context, p Project) error {
	sql := "UPSERT $rid CONTENT $doc"
	vars := map[string]any{
		"rid": surrealmodels.NewRecordID("projects", p.ID),
		"doc": p,
	}
	if _, err := surrealdb.Query[[]Project](ctx, s.db, sql, vars); err != nil {
		return fmt.Errorf("project: upsert: %w", err)
	}
	return nil
}

// SetWorkspaceID implements Store for SurrealStore.
func (s *SurrealStore) SetWorkspaceID(ctx context.Context, id, workspaceID string, now time.Time) (Project, error) {
	existing, err := s.GetByID(ctx, id, nil)
	if err != nil {
		return Project{}, err
	}
	existing.WorkspaceID = workspaceID
	existing.UpdatedAt = now
	if err := s.write(ctx, existing); err != nil {
		return Project{}, err
	}
	return existing, nil
}

// ListAll returns every row regardless of owner / workspace / status.
// Migration-only escape hatch — not on the Store interface. Callers use
// a type assertion to detect support.
func (s *SurrealStore) ListAll(ctx context.Context) ([]Project, error) {
	sql := fmt.Sprintf("SELECT %s FROM projects ORDER BY created_at ASC", selectCols)
	results, err := surrealdb.Query[[]Project](ctx, s.db, sql, nil)
	if err != nil {
		return nil, fmt.Errorf("project: list all: %w", err)
	}
	if results == nil || len(*results) == 0 {
		return []Project{}, nil
	}
	return (*results)[0].Result, nil
}

// ListMissingWorkspaceID implements Store for SurrealStore.
func (s *SurrealStore) ListMissingWorkspaceID(ctx context.Context) ([]Project, error) {
	sql := fmt.Sprintf("SELECT %s FROM projects WHERE workspace_id IS NONE OR workspace_id = '' ORDER BY created_at ASC", selectCols)
	results, err := surrealdb.Query[[]Project](ctx, s.db, sql, nil)
	if err != nil {
		return nil, fmt.Errorf("project: list missing workspace_id: %w", err)
	}
	if results == nil || len(*results) == 0 {
		return []Project{}, nil
	}
	return (*results)[0].Result, nil
}

// Compile-time assertion that SurrealStore satisfies Store.
var _ Store = (*SurrealStore)(nil)
