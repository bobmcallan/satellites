package repo

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// ErrNotFound is returned when a repo lookup misses or the caller's
// memberships exclude the row's workspace.
var ErrNotFound = errors.New("repo: not found")

// ErrAlreadyExists is returned by Create when a repo already exists on
// the project. The "one repo per project" invariant ignores status —
// archiving is not a free slot for a fresh remote per architecture §7
// ("each project has exactly zero or one repo record").
var ErrAlreadyExists = errors.New("repo: already exists for project")

// ErrInvalidStatus is returned when a write supplies a status outside
// the documented enum. Keeping the validator at the store layer means
// neither MCP handlers nor task workers can poke a row into an unknown
// state.
var ErrInvalidStatus = errors.New("repo: invalid status")

// Store is the persistence surface for repo rows. The verb set is the
// minimum needed by slice 12.1; richer query verbs (search, file, …)
// proxy to jcodemunch from the MCP layer in slice 12.2 and never go
// through this interface.
//
// memberships follows the project-wide convention: nil = no scoping
// (boot/backfill paths), empty = deny-all, non-empty = workspace_id IN
// memberships.
type Store interface {
	Create(ctx context.Context, r Repo, now time.Time) (Repo, error)
	GetByID(ctx context.Context, id string, memberships []string) (Repo, error)
	List(ctx context.Context, projectID string, memberships []string) ([]Repo, error)
	GetByRemote(ctx context.Context, workspaceID, gitRemote string) (Repo, error)
	UpdateIndexState(ctx context.Context, id, headSHA string, lastIndexedAt time.Time, symbolCount, fileCount int) (Repo, error)
	Archive(ctx context.Context, id string) (Repo, error)
}

// validateStatus rejects writes that supply a status outside the
// documented enum. Empty defaults to active in Create per the §7
// "Status: enum(active, archived)" definition.
func validateStatus(s string) error {
	if !IsKnownStatus(s) {
		return fmt.Errorf("%w: %q", ErrInvalidStatus, s)
	}
	return nil
}

// inMemberships is the shared workspace-scope predicate.
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

// MemoryStore is a concurrency-safe in-process Store used by unit tests
// and the local-iteration substrate per pr_local_iteration. The
// one-per-project invariant and status-enum validator live in this
// shared type so the surreal impl can defer to the same checks.
type MemoryStore struct {
	mu   sync.Mutex
	rows map[string]Repo
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{rows: make(map[string]Repo)}
}

// Create implements Store for MemoryStore.
func (m *MemoryStore) Create(ctx context.Context, r Repo, now time.Time) (Repo, error) {
	if r.ProjectID == "" {
		return Repo{}, fmt.Errorf("repo: project_id is required")
	}
	if r.GitRemote == "" {
		return Repo{}, fmt.Errorf("repo: git_remote is required")
	}
	if r.Status == "" {
		r.Status = StatusActive
	}
	if err := validateStatus(r.Status); err != nil {
		return Repo{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.rows {
		if existing.ProjectID == r.ProjectID {
			return Repo{}, fmt.Errorf("%w: project=%s existing=%s", ErrAlreadyExists, r.ProjectID, existing.ID)
		}
	}
	r.ID = NewID()
	r.CreatedAt = now
	r.UpdatedAt = now
	m.rows[r.ID] = r
	return r, nil
}

// GetByID implements Store for MemoryStore.
func (m *MemoryStore) GetByID(ctx context.Context, id string, memberships []string) (Repo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[id]
	if !ok {
		return Repo{}, ErrNotFound
	}
	if !inMemberships(r.WorkspaceID, memberships) {
		return Repo{}, ErrNotFound
	}
	return r, nil
}

// List implements Store for MemoryStore. Rows are ordered by CreatedAt
// ascending so callers see the project's first-registered remote first.
func (m *MemoryStore) List(ctx context.Context, projectID string, memberships []string) ([]Repo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Repo, 0)
	for _, r := range m.rows {
		if r.ProjectID != projectID {
			continue
		}
		if !inMemberships(r.WorkspaceID, memberships) {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

// GetByRemote implements Store for MemoryStore. The lookup is
// workspace-scoped; the same git_remote may appear in two workspaces
// without colliding (tenancy isolation per pr_0779e5af).
func (m *MemoryStore) GetByRemote(ctx context.Context, workspaceID, gitRemote string) (Repo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.rows {
		if r.WorkspaceID == workspaceID && r.GitRemote == gitRemote {
			return r, nil
		}
	}
	return Repo{}, ErrNotFound
}

// UpdateIndexState implements Store for MemoryStore. Bumps
// IndexVersion by one so subscribers can detect a fresh index without
// holding a snapshot per docs/architecture.md §7 ("index_version").
func (m *MemoryStore) UpdateIndexState(ctx context.Context, id, headSHA string, lastIndexedAt time.Time, symbolCount, fileCount int) (Repo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[id]
	if !ok {
		return Repo{}, ErrNotFound
	}
	r.HeadSHA = headSHA
	r.LastIndexedAt = lastIndexedAt
	r.SymbolCount = symbolCount
	r.FileCount = fileCount
	r.IndexVersion = r.IndexVersion + 1
	r.UpdatedAt = lastIndexedAt
	m.rows[id] = r
	return r, nil
}

// Archive implements Store for MemoryStore. Idempotent: archiving an
// already-archived row is a no-op return of the current state, not an
// error.
func (m *MemoryStore) Archive(ctx context.Context, id string) (Repo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[id]
	if !ok {
		return Repo{}, ErrNotFound
	}
	if r.Status == StatusArchived {
		return r, nil
	}
	r.Status = StatusArchived
	r.UpdatedAt = time.Now().UTC()
	m.rows[id] = r
	return r, nil
}

// Compile-time assertion.
var _ Store = (*MemoryStore)(nil)
