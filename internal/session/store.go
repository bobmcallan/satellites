package session

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrNotFound is returned when a session lookup misses.
var ErrNotFound = errors.New("session: not found")

// Store is the persistence surface for session registrations.
type Store interface {
	// Register upserts a session row for (userID, sessionID). Called by
	// the SessionStart hook at harness boot. LastSeenAt is set to now.
	Register(ctx context.Context, userID, sessionID, source string, now time.Time) (Session, error)

	// Get returns the session for (userID, sessionID) or ErrNotFound.
	Get(ctx context.Context, userID, sessionID string) (Session, error)

	// Touch refreshes LastSeenAt on the session; returns ErrNotFound if
	// the session is not registered. Called by the claim handler after
	// verifying the session is alive.
	Touch(ctx context.Context, userID, sessionID string, now time.Time) (Session, error)

	// SetOrchestratorGrant stamps the orchestrator_grant_id on an
	// existing session row. Called by the SessionStart hook path after
	// minting a role-grant for the freshly-registered session
	// (story_7d9c4b1b). Returns ErrNotFound if the session does not
	// exist.
	SetOrchestratorGrant(ctx context.Context, userID, sessionID, grantID string, now time.Time) (Session, error)

	// SetWorkspace stamps the workspace_id on an existing session row.
	// Called by session_register when the caller supplies a workspace_id
	// (e.g. via .mcp.json default_workspace) and by future verbs that
	// resolve a workspace from a project. Returns ErrNotFound if the
	// session does not exist. story_798631fd.
	SetWorkspace(ctx context.Context, userID, sessionID, workspaceID string, now time.Time) (Session, error)

	// ListAll returns every registered session row. Used by the
	// boot-time CI grant-backfill migration in story_4608a82c to build a
	// session_id → orchestrator_grant_id lookup. Unscoped read — callers
	// must handle cross-user rows themselves.
	ListAll(ctx context.Context) ([]Session, error)
}

// MemoryStore is a concurrency-safe in-process Store used by unit tests.
type MemoryStore struct {
	mu   sync.Mutex
	rows map[string]Session
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{rows: make(map[string]Session)}
}

func memKey(userID, sessionID string) string {
	return userID + "|" + sessionID
}

// Register implements Store for MemoryStore.
func (m *MemoryStore) Register(ctx context.Context, userID, sessionID, source string, now time.Time) (Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := memKey(userID, sessionID)
	existing, ok := m.rows[key]
	if ok {
		existing.LastSeenAt = now
		if source != "" {
			existing.Source = source
		}
		m.rows[key] = existing
		return existing, nil
	}
	sess := Session{
		UserID:     userID,
		SessionID:  sessionID,
		Source:     source,
		Registered: now,
		LastSeenAt: now,
	}
	m.rows[key] = sess
	return sess, nil
}

// Get implements Store for MemoryStore.
func (m *MemoryStore) Get(ctx context.Context, userID, sessionID string) (Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.rows[memKey(userID, sessionID)]
	if !ok {
		return Session{}, ErrNotFound
	}
	return sess, nil
}

// Touch implements Store for MemoryStore.
func (m *MemoryStore) Touch(ctx context.Context, userID, sessionID string, now time.Time) (Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := memKey(userID, sessionID)
	sess, ok := m.rows[key]
	if !ok {
		return Session{}, ErrNotFound
	}
	sess.LastSeenAt = now
	m.rows[key] = sess
	return sess, nil
}

// ListAll implements Store for MemoryStore.
func (m *MemoryStore) ListAll(ctx context.Context) ([]Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Session, 0, len(m.rows))
	for _, sess := range m.rows {
		out = append(out, sess)
	}
	return out, nil
}

// SetOrchestratorGrant implements Store for MemoryStore.
func (m *MemoryStore) SetOrchestratorGrant(ctx context.Context, userID, sessionID, grantID string, now time.Time) (Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := memKey(userID, sessionID)
	sess, ok := m.rows[key]
	if !ok {
		return Session{}, ErrNotFound
	}
	sess.OrchestratorGrantID = grantID
	sess.LastSeenAt = now
	m.rows[key] = sess
	return sess, nil
}

// SetWorkspace implements Store for MemoryStore.
func (m *MemoryStore) SetWorkspace(ctx context.Context, userID, sessionID, workspaceID string, now time.Time) (Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := memKey(userID, sessionID)
	sess, ok := m.rows[key]
	if !ok {
		return Session{}, ErrNotFound
	}
	sess.WorkspaceID = workspaceID
	sess.LastSeenAt = now
	m.rows[key] = sess
	return sess, nil
}

// IsStale reports whether now - sess.LastSeenAt exceeds staleness.
// Centralised so the handler + tests agree on the boundary semantics.
func IsStale(sess Session, now time.Time, staleness time.Duration) bool {
	if staleness <= 0 {
		staleness = StalenessDefault
	}
	return now.Sub(sess.LastSeenAt) > staleness
}

// Compile-time assertion.
var _ Store = (*MemoryStore)(nil)
