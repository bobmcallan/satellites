package ledger

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

func (r *recorder) last() captured {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.events[len(r.events)-1]
}

type panickingPublisher struct{}

func (panickingPublisher) Publish(context.Context, string, string, string, any) {
	panic("subscriber exploded")
}

func TestLedger_Append_Publishes(t *testing.T) {
	store := NewMemoryStore()
	rec := &recorder{}
	store.SetPublisher(rec)

	entry, err := store.Append(context.Background(), LedgerEntry{
		WorkspaceID: "wksp_A",
		ProjectID:   "proj_1",
		Type:        TypeEvidence,
		Content:     "body",
		Tags:        []string{"kind:test"},
	}, time.Now().UTC())
	require.NoError(t, err)
	require.Equal(t, 1, rec.count())

	got := rec.last()
	assert.Equal(t, "ws:wksp_A", got.topic)
	assert.Equal(t, EventKindAppended, got.kind)
	assert.Equal(t, "wksp_A", got.workspaceID)

	payload, ok := got.data.(map[string]any)
	require.True(t, ok, "payload is map[string]any")
	assert.Equal(t, entry.ID, payload["ledger_id"])
	assert.Equal(t, "wksp_A", payload["workspace_id"])
	assert.Equal(t, "proj_1", payload["project_id"])
	assert.Equal(t, TypeEvidence, payload["type"])
}

func TestLedger_Dereference_Publishes(t *testing.T) {
	store := NewMemoryStore()
	rec := &recorder{}
	store.SetPublisher(rec)
	now := time.Now().UTC()

	target, err := store.Append(context.Background(), LedgerEntry{
		WorkspaceID: "wksp_A", ProjectID: "proj_1", Type: TypeEvidence, Content: "target",
	}, now)
	require.NoError(t, err)
	// one append event so far
	require.Equal(t, 1, rec.count())

	_, err = store.Dereference(context.Background(), target.ID, "obsoleted", "alice", now, nil)
	require.NoError(t, err)

	// Expect two additional events: the audit-row Append, then the Dereference.
	require.Eventually(t, func() bool { return rec.count() >= 3 }, time.Second, 10*time.Millisecond)

	events := rec.events
	// The final event must be the Dereference.
	var deref *captured
	for i := range events {
		if events[i].kind == EventKindDereferenced {
			e := events[i]
			deref = &e
		}
	}
	require.NotNil(t, deref, "dereference event recorded")
	assert.Equal(t, "ws:wksp_A", deref.topic)
	payload := deref.data.(map[string]any)
	assert.Equal(t, target.ID, payload["ledger_id"])
	assert.Equal(t, "obsoleted", payload["reason"])
}

func TestLedger_Append_PanicRecovered(t *testing.T) {
	store := NewMemoryStore()
	store.SetPublisher(panickingPublisher{})

	entry, err := store.Append(context.Background(), LedgerEntry{
		WorkspaceID: "wksp_A", ProjectID: "p", Type: TypeEvidence, Content: "x",
	}, time.Now().UTC())
	assert.NoError(t, err, "hook panic must not abort the mutation")
	assert.NotEmpty(t, entry.ID)
	// Row must be readable afterwards — mutation actually happened.
	got, err := store.GetByID(context.Background(), entry.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, entry.ID, got.ID)
}

func TestLedger_Emit_NoWorkspaceID_Skips(t *testing.T) {
	// Entries without WorkspaceID (e.g. system rows written before workspace
	// scoping was introduced) must not publish — there is no valid topic.
	store := NewMemoryStore()
	rec := &recorder{}
	store.SetPublisher(rec)

	_, err := store.Append(context.Background(), LedgerEntry{
		WorkspaceID: "", ProjectID: "p", Type: TypeEvidence, Content: "x",
	}, time.Now().UTC())
	require.NoError(t, err)
	assert.Equal(t, 0, rec.count(), "no publish for missing workspace_id")
}

// TestLedger_Append_StoryActivityEmit covers sty_e55f335e: a story-
// scoped row whose tags contain an activity-kind triggers a second
// publish under EventKindStoryActivity carrying the panel-render
// fields (content, kind, created_at).
func TestLedger_Append_StoryActivityEmit(t *testing.T) {
	store := NewMemoryStore()
	rec := &recorder{}
	store.SetPublisher(rec)
	storyRef := "sty_test_e55f"
	contractRef := "ci_test"
	createdAt := time.Date(2026, 5, 2, 6, 0, 0, 0, time.UTC)

	entry, err := store.Append(context.Background(), LedgerEntry{
		WorkspaceID: "wksp_A",
		ProjectID:   "proj_1",
		StoryID:     &storyRef,
		ContractID:  &contractRef,
		Type:        TypePlan,
		Tags:        []string{"kind:plan", "phase:orchestrator"},
		Content:     "orchestrator plan composed",
	}, createdAt)
	require.NoError(t, err)
	require.NotEmpty(t, entry.ID)
	require.Equal(t, 2, rec.count(), "expect ledger.append + story.activity.append")

	var append1, activity *captured
	for i := range rec.events {
		switch rec.events[i].kind {
		case EventKindAppended:
			e := rec.events[i]
			append1 = &e
		case EventKindStoryActivity:
			e := rec.events[i]
			activity = &e
		}
	}
	require.NotNil(t, append1, "ledger.append event missing")
	require.NotNil(t, activity, "story.activity.append event missing")
	assert.Equal(t, "ws:wksp_A", activity.topic)
	payload := activity.data.(map[string]any)
	assert.Equal(t, entry.ID, payload["ledger_id"])
	assert.Equal(t, storyRef, payload["story_id"])
	assert.Equal(t, contractRef, payload["contract_id"])
	assert.Equal(t, "kind:plan", payload["kind"])
	assert.Equal(t, "orchestrator plan composed", payload["content"])
	assert.Equal(t, createdAt.Format(time.RFC3339), payload["created_at"])
}

func TestLedger_Append_StoryActivityEmit_SkipsWhenNotStoryScoped(t *testing.T) {
	store := NewMemoryStore()
	rec := &recorder{}
	store.SetPublisher(rec)
	_, err := store.Append(context.Background(), LedgerEntry{
		WorkspaceID: "wksp_A", ProjectID: "p",
		Type: TypePlan, Tags: []string{"kind:plan"}, Content: "no story",
	}, time.Now().UTC())
	require.NoError(t, err)
	assert.Equal(t, 1, rec.count(), "non-story-scoped row should not double-emit")
	assert.Equal(t, EventKindAppended, rec.events[0].kind)
}

func TestLedger_Append_StoryActivityEmit_SkipsNonActivityKind(t *testing.T) {
	store := NewMemoryStore()
	rec := &recorder{}
	store.SetPublisher(rec)
	storyRef := "sty_x"
	_, err := store.Append(context.Background(), LedgerEntry{
		WorkspaceID: "wksp_A", ProjectID: "p", StoryID: &storyRef,
		Type: TypeDecision, Tags: []string{"kind:something-else"}, Content: "x",
	}, time.Now().UTC())
	require.NoError(t, err)
	assert.Equal(t, 1, rec.count(), "row whose kind is outside the activity set should not double-emit")
	assert.Equal(t, EventKindAppended, rec.events[0].kind)
}
