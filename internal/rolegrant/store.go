package rolegrant

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
)

// ErrNotFound is returned when a role-grant lookup misses.
var ErrNotFound = errors.New("rolegrant: not found")

// ErrDanglingRole is returned when a write references a RoleID that does
// not resolve to an active type=role document visible in the caller's
// memberships (or scope=system).
var ErrDanglingRole = errors.New("rolegrant: role_id does not resolve to an active type=role document")

// ErrDanglingAgent is returned when a write references an AgentID that
// does not resolve to an active type=agent document.
var ErrDanglingAgent = errors.New("rolegrant: agent_id does not resolve to an active type=agent document")

// ErrAlreadyReleased is returned when Release is called on a grant that
// is already released. Release is idempotent at the call site but the
// store returns this to let callers distinguish the first release from
// subsequent ones for ledger tagging.
var ErrAlreadyReleased = errors.New("rolegrant: grant is already released")

// ListOptions are the structured filters consumed by Store.List. Filters
// compose with AND; empty fields mean "don't filter".
type ListOptions struct {
	RoleID      string
	AgentID     string
	GranteeKind string
	GranteeID   string
	Status      string // "" = any; use StatusActive / StatusReleased to narrow
	Limit       int
}

// Store is the persistence surface for role-grants. SurrealStore is the
// production implementation; MemoryStore is the in-process test double.
//
// Workspace scoping is enforced on every read via the memberships slice
// per docs/architecture.md §8: nil = no scoping, empty = deny-all,
// non-empty = workspace_id IN memberships.
type Store interface {
	// Create writes a new role-grant. RoleID + AgentID are validated
	// against the document store (must be active, types role + agent).
	// Status defaults to StatusActive; IssuedAt defaults to now if zero.
	Create(ctx context.Context, g RoleGrant, now time.Time) (RoleGrant, error)

	// GetByID returns the grant with the given id, or ErrNotFound.
	GetByID(ctx context.Context, id string, memberships []string) (RoleGrant, error)

	// List returns grants matching opts ordered by IssuedAt DESC, capped
	// at opts.Limit (0 = unlimited).
	List(ctx context.Context, opts ListOptions, memberships []string) ([]RoleGrant, error)

	// Release transitions an active grant to status=released, records
	// note on the row, and stamps ReleasedAt=now. Returns
	// ErrAlreadyReleased (with the grant row) when called on a released
	// grant so callers can ledger-tag the redundant call.
	Release(ctx context.Context, id, note string, now time.Time, memberships []string) (RoleGrant, error)
}

// MemoryStore is a concurrency-safe in-process Store used by unit tests.
// It depends on a document.Store for FK resolution on RoleID + AgentID.
type MemoryStore struct {
	mu   sync.Mutex
	rows map[string]RoleGrant
	docs document.Store
}

// NewMemoryStore returns an empty MemoryStore. docs is required — Create
// needs it to resolve RoleID + AgentID FKs. Passing nil panics.
func NewMemoryStore(docs document.Store) *MemoryStore {
	if docs == nil {
		panic("rolegrant.MemoryStore requires a non-nil document.Store")
	}
	return &MemoryStore{
		rows: make(map[string]RoleGrant),
		docs: docs,
	}
}

// Create implements Store for MemoryStore.
func (m *MemoryStore) Create(ctx context.Context, g RoleGrant, now time.Time) (RoleGrant, error) {
	if g.Status == "" {
		g.Status = StatusActive
	}
	if g.IssuedAt.IsZero() {
		g.IssuedAt = now
	}
	if err := g.Validate(); err != nil {
		return RoleGrant{}, err
	}
	if err := m.resolveFK(ctx, g); err != nil {
		return RoleGrant{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if g.ID == "" {
		g.ID = NewID()
	}
	if _, exists := m.rows[g.ID]; exists {
		return RoleGrant{}, fmt.Errorf("rolegrant: id %q already exists", g.ID)
	}
	g.CreatedAt = now
	g.UpdatedAt = now
	m.rows[g.ID] = g
	return g, nil
}

// GetByID implements Store for MemoryStore.
func (m *MemoryStore) GetByID(ctx context.Context, id string, memberships []string) (RoleGrant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.rows[id]
	if !ok {
		return RoleGrant{}, ErrNotFound
	}
	if !workspaceVisible(g.WorkspaceID, memberships) {
		return RoleGrant{}, ErrNotFound
	}
	return g, nil
}

// List implements Store for MemoryStore.
func (m *MemoryStore) List(ctx context.Context, opts ListOptions, memberships []string) ([]RoleGrant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]RoleGrant, 0, len(m.rows))
	for _, g := range m.rows {
		if !workspaceVisible(g.WorkspaceID, memberships) {
			continue
		}
		if opts.RoleID != "" && g.RoleID != opts.RoleID {
			continue
		}
		if opts.AgentID != "" && g.AgentID != opts.AgentID {
			continue
		}
		if opts.GranteeKind != "" && g.GranteeKind != opts.GranteeKind {
			continue
		}
		if opts.GranteeID != "" && g.GranteeID != opts.GranteeID {
			continue
		}
		if opts.Status != "" && g.Status != opts.Status {
			continue
		}
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].IssuedAt.After(out[j].IssuedAt)
	})
	if opts.Limit > 0 && len(out) > opts.Limit {
		out = out[:opts.Limit]
	}
	return out, nil
}

// Release implements Store for MemoryStore.
func (m *MemoryStore) Release(ctx context.Context, id, note string, now time.Time, memberships []string) (RoleGrant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.rows[id]
	if !ok {
		return RoleGrant{}, ErrNotFound
	}
	if !workspaceVisible(g.WorkspaceID, memberships) {
		return RoleGrant{}, ErrNotFound
	}
	if g.Status == StatusReleased {
		return g, ErrAlreadyReleased
	}
	g.Status = StatusReleased
	g.ReleasedAt = timePtr(now)
	g.ReleaseNote = note
	g.UpdatedAt = now
	m.rows[id] = g
	return g, nil
}

// resolveFK confirms AgentID resolves to an active type=agent document.
// RoleID is validated only when supplied (epic:roleless-agents — the
// role tier is now optional; agents are the sole capability tier).
func (m *MemoryStore) resolveFK(ctx context.Context, g RoleGrant) error {
	if g.RoleID != "" {
		role, err := m.docs.GetByID(ctx, g.RoleID, nil)
		if err != nil || role.Type != document.TypeRole || role.Status != document.StatusActive {
			return ErrDanglingRole
		}
	}
	agent, err := m.docs.GetByID(ctx, g.AgentID, nil)
	if err != nil || agent.Type != document.TypeAgent || agent.Status != document.StatusActive {
		return ErrDanglingAgent
	}
	return nil
}

// workspaceVisible returns true when workspaceID is in memberships (or
// memberships is nil which means "no scoping"). An empty non-nil
// memberships slice denies every workspace.
func workspaceVisible(workspaceID string, memberships []string) bool {
	if memberships == nil {
		return true
	}
	for _, m := range memberships {
		if m == workspaceID {
			return true
		}
	}
	return false
}

func timePtr(t time.Time) *time.Time {
	return &t
}
