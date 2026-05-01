package changelog

import (
	"context"
	"sort"
	"sync"
	"time"
)

// ListOptions filters a List call. Service is exact match; ProjectID is
// required (the store is project-scoped). Limit caps the result set;
// zero falls back to defaultLimit.
type ListOptions struct {
	ProjectID string
	Service   string
	Limit     int
}

// UpdateFields names the per-call mutable subset for Update. Only the
// content/version/effective_date columns are mutable — service +
// project + workspace identity is set at Create.
type UpdateFields struct {
	VersionFrom   *string
	VersionTo     *string
	Content       *string
	EffectiveDate *time.Time
}

const (
	defaultLimit = 50
	maxLimit     = 500
)

// Store is the persistence surface. The memberships filter mirrors the
// pattern used by stories/documents/ledger: nil = no filter, empty =
// deny-all, non-empty = workspace_id IN memberships.
type Store interface {
	Create(ctx context.Context, c Changelog, now time.Time) (Changelog, error)
	GetByID(ctx context.Context, id string, memberships []string) (Changelog, error)
	List(ctx context.Context, opts ListOptions, memberships []string) ([]Changelog, error)
	Update(ctx context.Context, id string, fields UpdateFields, now time.Time, memberships []string) (Changelog, error)
	Delete(ctx context.Context, id string, memberships []string) error
}

// MemoryStore is a concurrency-safe in-process Store used by tests +
// dev environments. Newest-first List ordering: CreatedAt desc, with
// ID as a stable tie-breaker.
type MemoryStore struct {
	mu   sync.Mutex
	rows map[string]Changelog
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{rows: make(map[string]Changelog)}
}

func (m *MemoryStore) Create(ctx context.Context, c Changelog, now time.Time) (Changelog, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c.ID = NewID()
	c.CreatedAt = now
	c.UpdatedAt = now
	m.rows[c.ID] = c
	return c, nil
}

func (m *MemoryStore) GetByID(ctx context.Context, id string, memberships []string) (Changelog, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.rows[id]
	if !ok {
		return Changelog{}, ErrNotFound
	}
	if !inMemberships(c.WorkspaceID, memberships) {
		return Changelog{}, ErrNotFound
	}
	return c, nil
}

func (m *MemoryStore) List(ctx context.Context, opts ListOptions, memberships []string) ([]Changelog, error) {
	opts = opts.normalised()
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Changelog, 0)
	for _, c := range m.rows {
		if opts.ProjectID != "" && c.ProjectID != opts.ProjectID {
			continue
		}
		if opts.Service != "" && c.Service != opts.Service {
			continue
		}
		if !inMemberships(c.WorkspaceID, memberships) {
			continue
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	if len(out) > opts.Limit {
		out = out[:opts.Limit]
	}
	return out, nil
}

func (m *MemoryStore) Update(ctx context.Context, id string, fields UpdateFields, now time.Time, memberships []string) (Changelog, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.rows[id]
	if !ok {
		return Changelog{}, ErrNotFound
	}
	if !inMemberships(c.WorkspaceID, memberships) {
		return Changelog{}, ErrNotFound
	}
	applyUpdate(&c, fields)
	c.UpdatedAt = now
	m.rows[id] = c
	return c, nil
}

func (m *MemoryStore) Delete(ctx context.Context, id string, memberships []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.rows[id]
	if !ok {
		return ErrNotFound
	}
	if !inMemberships(c.WorkspaceID, memberships) {
		return ErrNotFound
	}
	delete(m.rows, id)
	return nil
}

func (o ListOptions) normalised() ListOptions {
	if o.Limit <= 0 {
		o.Limit = defaultLimit
	}
	if o.Limit > maxLimit {
		o.Limit = maxLimit
	}
	return o
}

// inMemberships is the shared workspace-membership predicate (parity
// with internal/story.inStoryMemberships).
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

// applyUpdate copies non-nil pointers from fields onto c. Shared by
// Memory + Surreal stores so the semantics stay aligned.
func applyUpdate(c *Changelog, fields UpdateFields) {
	if fields.VersionFrom != nil {
		c.VersionFrom = *fields.VersionFrom
	}
	if fields.VersionTo != nil {
		c.VersionTo = *fields.VersionTo
	}
	if fields.Content != nil {
		c.Content = *fields.Content
	}
	if fields.EffectiveDate != nil {
		c.EffectiveDate = *fields.EffectiveDate
	}
}

// Compile-time assertion.
var _ Store = (*MemoryStore)(nil)
