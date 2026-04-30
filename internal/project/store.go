package project

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// ErrNotFound is returned when a project lookup misses.
var ErrNotFound = errors.New("project: not found")

// ErrDuplicateGitRemote is returned by Create when the supplied
// (workspace_id, git_remote) tuple is already taken. The MCP layer maps
// this to a clear "duplicate remote" error so callers can recover by
// fetching the existing project rather than silently creating a second
// row pointing at the same repo.
var ErrDuplicateGitRemote = errors.New("project: git_remote already registered in workspace")

// Store is the persistence surface for projects. SurrealStore is the
// production implementation; MemoryStore is the in-process test double.
type Store interface {
	// Create persists a new Project. The caller supplies ownerUserID +
	// workspaceID + name; the store mints the id, stamps CreatedAt/UpdatedAt,
	// and sets Status to StatusActive. An empty workspaceID is permitted at
	// write time so bootstrap + legacy paths can run; the boot-time backfill
	// stamps empty rows with the owner's default workspace.
	//
	// Deprecated: prefer CreateWithRemote so the canonical (workspace,
	// git_remote) identity can be enforced. Kept for callers that
	// intentionally track no remote.
	Create(ctx context.Context, ownerUserID, workspaceID, name string, now time.Time) (Project, error)

	// CreateWithRemote persists a new Project keyed on (workspace_id,
	// git_remote). Empty git_remote behaves like Create. A non-empty
	// git_remote that already exists in workspaceID returns
	// ErrDuplicateGitRemote — callers should prefer GetByGitRemote in that
	// case.
	CreateWithRemote(ctx context.Context, ownerUserID, workspaceID, name, gitRemote string, now time.Time) (Project, error)

	// GetByID returns the project with the given id, or ErrNotFound. When
	// memberships is non-nil the row must carry a workspace_id that appears
	// in the slice; non-member rows return ErrNotFound (the same shape a
	// missing row would). nil memberships disable scoping (bootstrap and
	// backfill paths that must see every row).
	GetByID(ctx context.Context, id string, memberships []string) (Project, error)

	// GetByGitRemote returns the project in workspaceID whose git_remote
	// matches, or ErrNotFound. Used by repo_add and project_create to
	// dedupe and by the MCP layer to resolve a remote → project_id.
	GetByGitRemote(ctx context.Context, workspaceID, gitRemote string) (Project, error)

	// ListByOwner returns the owner's projects, newest-first by CreatedAt.
	// memberships scoping matches GetByID semantics: nil = no scoping,
	// empty = deny-all, non-empty = workspace_id IN memberships.
	ListByOwner(ctx context.Context, ownerUserID string, memberships []string) ([]Project, error)

	// UpdateName renames an existing project and bumps UpdatedAt. Returns the
	// updated Project. ErrNotFound on missing id.
	UpdateName(ctx context.Context, id, name string, now time.Time) (Project, error)

	// SetGitRemote stamps git_remote on an existing project. Returns
	// ErrDuplicateGitRemote when (workspace_id, git_remote) is already
	// taken by a different row.
	SetGitRemote(ctx context.Context, id, gitRemote string, now time.Time) (Project, error)

	// SetStatus flips a project's status (active ↔ archived). Soft-delete
	// path; rows are never physically removed. Returns the updated Project.
	SetStatus(ctx context.Context, id, status string, now time.Time) (Project, error)

	// SetWorkspaceID stamps workspaceID on an existing project. Used by the
	// boot-time backfill to migrate rows that pre-date workspace scoping.
	SetWorkspaceID(ctx context.Context, id, workspaceID string, now time.Time) (Project, error)

	// ListMissingWorkspaceID returns rows whose workspace_id is empty.
	// Backfill uses this to find work to do.
	ListMissingWorkspaceID(ctx context.Context) ([]Project, error)
}

// MemoryStore is a concurrency-safe in-process Store used by unit tests.
type MemoryStore struct {
	mu   sync.Mutex
	rows map[string]Project // key = id
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{rows: make(map[string]Project)}
}

// Create implements Store for MemoryStore.
func (m *MemoryStore) Create(ctx context.Context, ownerUserID, workspaceID, name string, now time.Time) (Project, error) {
	return m.CreateWithRemote(ctx, ownerUserID, workspaceID, name, "", now)
}

// CreateWithRemote implements Store for MemoryStore.
func (m *MemoryStore) CreateWithRemote(ctx context.Context, ownerUserID, workspaceID, name, gitRemote string, now time.Time) (Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if gitRemote != "" {
		for _, existing := range m.rows {
			if existing.WorkspaceID == workspaceID && existing.GitRemote == gitRemote && existing.Status == StatusActive {
				return Project{}, ErrDuplicateGitRemote
			}
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
	m.rows[p.ID] = p
	return p, nil
}

// GetByGitRemote implements Store for MemoryStore.
func (m *MemoryStore) GetByGitRemote(ctx context.Context, workspaceID, gitRemote string) (Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.rows {
		if p.WorkspaceID == workspaceID && p.GitRemote == gitRemote && p.Status == StatusActive {
			return p, nil
		}
	}
	return Project{}, ErrNotFound
}

// GetByID implements Store for MemoryStore.
func (m *MemoryStore) GetByID(ctx context.Context, id string, memberships []string) (Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.rows[id]
	if !ok {
		return Project{}, ErrNotFound
	}
	if !inMemberships(p.WorkspaceID, memberships) {
		return Project{}, ErrNotFound
	}
	return p, nil
}

// ListByOwner implements Store for MemoryStore.
func (m *MemoryStore) ListByOwner(ctx context.Context, ownerUserID string, memberships []string) ([]Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Project, 0)
	for _, p := range m.rows {
		if p.OwnerUserID != ownerUserID {
			continue
		}
		if !inMemberships(p.WorkspaceID, memberships) {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

// inMemberships is the shared membership-filter predicate. nil = no filter
// (seed/backfill paths); empty slice = deny-all; non-empty = row passes if
// its workspace_id is in the slice.
func inMemberships(wsID string, memberships []string) bool {
	if memberships == nil {
		return true
	}
	for _, m := range memberships {
		if m == wsID {
			return true
		}
	}
	return false
}

// UpdateName implements Store for MemoryStore.
func (m *MemoryStore) UpdateName(ctx context.Context, id, name string, now time.Time) (Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.rows[id]
	if !ok {
		return Project{}, ErrNotFound
	}
	p.Name = name
	p.UpdatedAt = now
	m.rows[id] = p
	return p, nil
}

// SetGitRemote implements Store for MemoryStore.
func (m *MemoryStore) SetGitRemote(ctx context.Context, id, gitRemote string, now time.Time) (Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.rows[id]
	if !ok {
		return Project{}, ErrNotFound
	}
	if gitRemote != "" {
		for _, existing := range m.rows {
			if existing.ID == id {
				continue
			}
			if existing.WorkspaceID == p.WorkspaceID && existing.GitRemote == gitRemote && existing.Status == StatusActive {
				return Project{}, ErrDuplicateGitRemote
			}
		}
	}
	p.GitRemote = gitRemote
	p.UpdatedAt = now
	m.rows[id] = p
	return p, nil
}

// SetStatus implements Store for MemoryStore.
func (m *MemoryStore) SetStatus(ctx context.Context, id, status string, now time.Time) (Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.rows[id]
	if !ok {
		return Project{}, ErrNotFound
	}
	p.Status = status
	p.UpdatedAt = now
	m.rows[id] = p
	return p, nil
}

// SetWorkspaceID implements Store for MemoryStore.
func (m *MemoryStore) SetWorkspaceID(ctx context.Context, id, workspaceID string, now time.Time) (Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.rows[id]
	if !ok {
		return Project{}, ErrNotFound
	}
	p.WorkspaceID = workspaceID
	p.UpdatedAt = now
	m.rows[id] = p
	return p, nil
}

// ListAll returns every row regardless of owner / workspace / status.
// Migration-only escape hatch — not on the Store interface. Callers use
// a type assertion to detect support.
func (m *MemoryStore) ListAll(ctx context.Context) ([]Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Project, 0, len(m.rows))
	for _, p := range m.rows {
		out = append(out, p)
	}
	return out, nil
}

// ListMissingWorkspaceID implements Store for MemoryStore.
func (m *MemoryStore) ListMissingWorkspaceID(ctx context.Context) ([]Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Project, 0)
	for _, p := range m.rows {
		if p.WorkspaceID == "" {
			out = append(out, p)
		}
	}
	return out, nil
}

// Compile-time assertion that MemoryStore satisfies Store.
var _ Store = (*MemoryStore)(nil)
