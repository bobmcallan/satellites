package rolegrant

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/surrealdb/surrealdb.go"
	surrealmodels "github.com/surrealdb/surrealdb.go/pkg/models"

	"github.com/bobmcallan/satellites/internal/document"
)

// SurrealStore is a SurrealDB-backed Store. docs resolves the RoleID +
// AgentID FKs at Create time.
type SurrealStore struct {
	db   *surrealdb.DB
	docs document.Store
}

// NewSurrealStore wraps db as a Store. Defines the `role_grants` table
// schemaless and the three indexes covering the expected query paths.
// Panics if docs is nil — Create cannot proceed without FK resolution.
func NewSurrealStore(db *surrealdb.DB, docs document.Store) *SurrealStore {
	if docs == nil {
		panic("rolegrant.SurrealStore requires a non-nil document.Store")
	}
	s := &SurrealStore{db: db, docs: docs}
	_, _ = surrealdb.Query[any](context.Background(), db, "DEFINE TABLE IF NOT EXISTS role_grants SCHEMALESS", nil)
	_, _ = surrealdb.Query[any](context.Background(), db, "DEFINE INDEX IF NOT EXISTS role_grants_grantee ON TABLE role_grants FIELDS workspace_id, grantee_kind, grantee_id, status", nil)
	_, _ = surrealdb.Query[any](context.Background(), db, "DEFINE INDEX IF NOT EXISTS role_grants_role ON TABLE role_grants FIELDS workspace_id, role_id, status", nil)
	_, _ = surrealdb.Query[any](context.Background(), db, "DEFINE INDEX IF NOT EXISTS role_grants_agent ON TABLE role_grants FIELDS workspace_id, agent_id, status", nil)
	return s
}

const selectCols = "meta::id(id) AS id, workspace_id, project_id, role_id, agent_id, grantee_kind, grantee_id, status, issued_at, expires_at, released_at, release_note, created_at, updated_at"

// Create implements Store for SurrealStore.
func (s *SurrealStore) Create(ctx context.Context, g RoleGrant, now time.Time) (RoleGrant, error) {
	if g.Status == "" {
		g.Status = StatusActive
	}
	if g.IssuedAt.IsZero() {
		g.IssuedAt = now
	}
	if err := g.Validate(); err != nil {
		return RoleGrant{}, err
	}
	role, err := s.docs.GetByID(ctx, g.RoleID, nil)
	if err != nil || role.Type != document.TypeRole || role.Status != document.StatusActive {
		return RoleGrant{}, ErrDanglingRole
	}
	agent, err := s.docs.GetByID(ctx, g.AgentID, nil)
	if err != nil || agent.Type != document.TypeAgent || agent.Status != document.StatusActive {
		return RoleGrant{}, ErrDanglingAgent
	}
	if g.ID == "" {
		g.ID = NewID()
	}
	g.CreatedAt = now
	g.UpdatedAt = now
	if err := s.write(ctx, g); err != nil {
		return RoleGrant{}, err
	}
	return g, nil
}

// GetByID implements Store for SurrealStore.
func (s *SurrealStore) GetByID(ctx context.Context, id string, memberships []string) (RoleGrant, error) {
	if memberships != nil && len(memberships) == 0 {
		return RoleGrant{}, ErrNotFound
	}
	conds := []string{"id = $rid"}
	vars := map[string]any{"rid": surrealmodels.NewRecordID("role_grants", id)}
	if memberships != nil {
		conds = append(conds, "workspace_id IN $memberships")
		vars["memberships"] = memberships
	}
	sql := fmt.Sprintf("SELECT %s FROM role_grants WHERE %s LIMIT 1", selectCols, strings.Join(conds, " AND "))
	results, err := surrealdb.Query[[]RoleGrant](ctx, s.db, sql, vars)
	if err != nil {
		return RoleGrant{}, fmt.Errorf("rolegrant: select by id: %w", err)
	}
	if results == nil || len(*results) == 0 || len((*results)[0].Result) == 0 {
		return RoleGrant{}, ErrNotFound
	}
	return (*results)[0].Result[0], nil
}

// List implements Store for SurrealStore.
func (s *SurrealStore) List(ctx context.Context, opts ListOptions, memberships []string) ([]RoleGrant, error) {
	if memberships != nil && len(memberships) == 0 {
		return []RoleGrant{}, nil
	}
	conds := []string{}
	vars := map[string]any{}
	if opts.RoleID != "" {
		conds = append(conds, "role_id = $role")
		vars["role"] = opts.RoleID
	}
	if opts.AgentID != "" {
		conds = append(conds, "agent_id = $agent")
		vars["agent"] = opts.AgentID
	}
	if opts.GranteeKind != "" {
		conds = append(conds, "grantee_kind = $gkind")
		vars["gkind"] = opts.GranteeKind
	}
	if opts.GranteeID != "" {
		conds = append(conds, "grantee_id = $gid")
		vars["gid"] = opts.GranteeID
	}
	if opts.Status != "" {
		conds = append(conds, "status = $st")
		vars["st"] = opts.Status
	}
	if memberships != nil {
		conds = append(conds, "workspace_id IN $memberships")
		vars["memberships"] = memberships
	}
	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}
	limit := ""
	if opts.Limit > 0 {
		limit = fmt.Sprintf(" LIMIT %d", opts.Limit)
	}
	sql := fmt.Sprintf("SELECT %s FROM role_grants %s ORDER BY issued_at DESC%s", selectCols, where, limit)
	results, err := surrealdb.Query[[]RoleGrant](ctx, s.db, sql, vars)
	if err != nil {
		return nil, fmt.Errorf("rolegrant: list: %w", err)
	}
	if results == nil || len(*results) == 0 {
		return []RoleGrant{}, nil
	}
	return (*results)[0].Result, nil
}

// Release implements Store for SurrealStore.
func (s *SurrealStore) Release(ctx context.Context, id, note string, now time.Time, memberships []string) (RoleGrant, error) {
	g, err := s.GetByID(ctx, id, memberships)
	if err != nil {
		return RoleGrant{}, err
	}
	if g.Status == StatusReleased {
		return g, ErrAlreadyReleased
	}
	g.Status = StatusReleased
	g.ReleasedAt = timePtr(now)
	g.ReleaseNote = note
	g.UpdatedAt = now
	if err := s.write(ctx, g); err != nil {
		return RoleGrant{}, err
	}
	return g, nil
}

func (s *SurrealStore) write(ctx context.Context, g RoleGrant) error {
	sql := "UPSERT $rid CONTENT $doc"
	vars := map[string]any{
		"rid": surrealmodels.NewRecordID("role_grants", g.ID),
		"doc": g,
	}
	if _, err := surrealdb.Query[[]RoleGrant](ctx, s.db, sql, vars); err != nil {
		return fmt.Errorf("rolegrant: upsert: %w", err)
	}
	return nil
}

// Compile-time assertion.
var _ Store = (*SurrealStore)(nil)
