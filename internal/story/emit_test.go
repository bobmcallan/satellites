package story

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobmcallan/satellites/internal/ledger"
)

type captured struct {
	topic       string
	kind        string
	workspaceID string
	data        any
}

type recorder struct {
	mu     sync.Mutex
	events []captured
}

func (r *recorder) Publish(_ context.Context, topic, kind, workspaceID string, data any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, captured{topic: topic, kind: kind, workspaceID: workspaceID, data: data})
}

func (r *recorder) count() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.events) }

type panickingPub struct{}

func (panickingPub) Publish(context.Context, string, string, string, any) {
	panic("subscriber exploded")
}

func TestStory_UpdateStatus_Publishes(t *testing.T) {
	led := ledger.NewMemoryStore()
	store := NewMemoryStore(led)
	rec := &recorder{}
	store.SetPublisher(rec)
	ctx := context.Background()
	now := time.Now().UTC()

	s, err := store.Create(ctx, Story{
		WorkspaceID: "wksp_A",
		ProjectID:   "proj_1",
		Title:       "parent",
	}, now)
	require.NoError(t, err)
	// sty_1ff1065a: Create now emits story.backlog so the workspace
	// panel can append a fresh row without a refetch.
	require.Equal(t, 1, rec.count())
	require.Equal(t, "story.backlog", rec.events[0].kind)

	_, err = store.UpdateStatus(ctx, s.ID, StatusReady, "alice", now.Add(time.Second), nil)
	require.NoError(t, err)
	require.Equal(t, 2, rec.count())

	got := rec.events[1]
	assert.Equal(t, "ws:wksp_A", got.topic)
	assert.Equal(t, "story.ready", got.kind)
	payload := got.data.(map[string]any)
	assert.Equal(t, s.ID, payload["story_id"])
	assert.Equal(t, "parent", payload["title"])
}

// TestStory_Create_Publishes asserts the substrate emits a story.<status>
// event from Create so workspace WS subscribers can render a freshly-
// created row without a page reload (sty_1ff1065a).
func TestStory_Create_Publishes(t *testing.T) {
	led := ledger.NewMemoryStore()
	store := NewMemoryStore(led)
	rec := &recorder{}
	store.SetPublisher(rec)
	ctx := context.Background()
	now := time.Now().UTC()

	s, err := store.Create(ctx, Story{
		WorkspaceID: "wksp_A",
		ProjectID:   "proj_1",
		Title:       "fresh",
		Priority:    "high",
		Category:    "bug",
		Tags:        []string{"epic:status-bus-v1"},
	}, now)
	require.NoError(t, err)
	require.Equal(t, 1, rec.count())

	got := rec.events[0]
	assert.Equal(t, "ws:wksp_A", got.topic)
	assert.Equal(t, "story.backlog", got.kind)
	assert.Equal(t, "wksp_A", got.workspaceID)

	payload, ok := got.data.(map[string]any)
	require.True(t, ok, "payload must be map")
	assert.Equal(t, s.ID, payload["story_id"])
	assert.Equal(t, "wksp_A", payload["workspace_id"])
	assert.Equal(t, "proj_1", payload["project_id"])
	assert.Equal(t, "fresh", payload["title"])
	assert.Equal(t, "backlog", payload["status"])
	assert.Equal(t, "high", payload["priority"])
	assert.Equal(t, "bug", payload["category"])
	assert.Equal(t, []string{"epic:status-bus-v1"}, payload["tags"])
	assert.NotNil(t, payload["updated_at"])
}

// TestStory_Create_NoPublisher_NoCrash asserts Create is safe when no
// publisher is wired (test harnesses that never call SetPublisher).
func TestStory_Create_NoPublisher_NoCrash(t *testing.T) {
	led := ledger.NewMemoryStore()
	store := NewMemoryStore(led)
	// Deliberately no SetPublisher.
	ctx := context.Background()
	now := time.Now().UTC()

	_, err := store.Create(ctx, Story{
		WorkspaceID: "wksp_A", ProjectID: "proj_1", Title: "t",
	}, now)
	assert.NoError(t, err)
}

func TestStory_InvalidTransition_NoPublish(t *testing.T) {
	led := ledger.NewMemoryStore()
	store := NewMemoryStore(led)
	rec := &recorder{}
	store.SetPublisher(rec)
	ctx := context.Background()
	now := time.Now().UTC()

	s, err := store.Create(ctx, Story{
		WorkspaceID: "wksp_A", ProjectID: "proj_1", Title: "t",
	}, now)
	require.NoError(t, err)
	// Walk to done so the next transition is a terminal-rollback —
	// the only category the V3-parity matrix (sty_01f75142) rejects.
	_, err = store.UpdateStatus(ctx, s.ID, StatusDone, "alice", now.Add(time.Second), nil)
	require.NoError(t, err)
	priorCount := rec.count()

	// done → backlog is invalid (terminal source).
	_, err = store.UpdateStatus(ctx, s.ID, StatusBacklog, "alice", now.Add(2*time.Second), nil)
	assert.Error(t, err)
	assert.Equal(t, priorCount, rec.count(), "no publish on failed transition")
}

// TestStory_UpdateStatusDerived_BypassesTransitionGuard asserts the
// derived-write path (sty_dc121948) accepts transitions the manual
// path's forward-only walk rejects, and emits the ledger row + WS
// event with actor=system:reconciler + reason:derived tag.
func TestStory_UpdateStatusDerived_BypassesTransitionGuard(t *testing.T) {
	led := ledger.NewMemoryStore()
	store := NewMemoryStore(led)
	rec := &recorder{}
	store.SetPublisher(rec)
	ctx := context.Background()
	now := time.Now().UTC()

	s, err := store.Create(ctx, Story{
		WorkspaceID: "wksp_A", ProjectID: "proj_1", Title: "t",
	}, now)
	require.NoError(t, err)

	// Walk to done so we have a terminal source for the bypass test.
	// V3 parity (sty_01f75142) means terminal-rollback is the only
	// transition the manual path still rejects.
	_, err = store.UpdateStatus(ctx, s.ID, StatusDone, "alice", now.Add(time.Second), nil)
	require.NoError(t, err)

	// Manual path rejects done → backlog (terminal-rollback).
	_, err = store.UpdateStatus(ctx, s.ID, StatusBacklog, "alice", now.Add(2*time.Second), nil)
	require.Error(t, err, "manual path must still reject terminal-rollback")

	// Derived path accepts the same transition (bypasses the guard).
	updated, err := store.UpdateStatusDerived(ctx, s.ID, StatusBacklog, now.Add(3*time.Second), nil)
	require.NoError(t, err)
	assert.Equal(t, StatusBacklog, updated.Status)

	// Ledger row carries reason:derived and the system:reconciler actor.
	rows, err := led.List(ctx, "proj_1", ledger.ListOptions{
		Type: ledger.TypeDecision,
		Tags: []string{"kind:" + LedgerEntryType},
	}, nil)
	require.NoError(t, err)
	require.NotEmpty(t, rows)
	last := rows[0]
	assert.Equal(t, derivedActor, last.CreatedBy)
	hasReason := false
	for _, tag := range last.Tags {
		if tag == "reason:derived" {
			hasReason = true
		}
	}
	assert.True(t, hasReason, "derived ledger row must carry reason:derived tag")

	// WS event still publishes — the panel sees the flip.
	require.GreaterOrEqual(t, rec.count(), 2) // at least Create-emit + Derived-emit
	last2 := rec.events[rec.count()-1]
	assert.Equal(t, "story.backlog", last2.kind)
}

// TestStory_UpdateStatusDerived_NoOpWhenStatusMatches asserts a
// derived call whose target equals the current status is a no-op —
// no ledger row, no WS event. Idempotence under repeated bus events.
func TestStory_UpdateStatusDerived_NoOpWhenStatusMatches(t *testing.T) {
	led := ledger.NewMemoryStore()
	store := NewMemoryStore(led)
	rec := &recorder{}
	store.SetPublisher(rec)
	ctx := context.Background()
	now := time.Now().UTC()

	s, err := store.Create(ctx, Story{
		WorkspaceID: "wksp_A", ProjectID: "proj_1", Title: "t",
	}, now)
	require.NoError(t, err)
	priorEvents := rec.count()

	// Story is already at backlog; derived call to backlog is a no-op.
	got, err := store.UpdateStatusDerived(ctx, s.ID, StatusBacklog, now.Add(time.Second), nil)
	require.NoError(t, err)
	assert.Equal(t, StatusBacklog, got.Status)
	assert.Equal(t, priorEvents, rec.count(), "idempotent derived call must not emit")
}

func TestStory_PanicRecovered(t *testing.T) {
	led := ledger.NewMemoryStore()
	store := NewMemoryStore(led)
	store.SetPublisher(panickingPub{})
	ctx := context.Background()
	now := time.Now().UTC()

	s, err := store.Create(ctx, Story{
		WorkspaceID: "wksp_A", ProjectID: "proj_1", Title: "t",
	}, now)
	require.NoError(t, err)

	_, err = store.UpdateStatus(ctx, s.ID, StatusReady, "alice", now.Add(time.Second), nil)
	assert.NoError(t, err, "hook panic must not abort the mutation")
}
