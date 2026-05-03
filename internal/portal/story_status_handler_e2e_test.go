// sty_de9f10f9 — POST /api/stories/{id}/status end-to-end coverage:
// HTTP status + ledger row + WS event + store mutation, on both the
// happy and sad paths. The existing story_status_handler_test.go
// covers the HTTP+ledger axes only — neither WS event publication
// nor "no-op on rejection" was asserted, which is the gap a recent
// silent-failure incident exposed (story body, sty_de9f10f9).
package portal

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/story"
)

// e2eEvent is the captured shape of one WS publication.
type e2eEvent struct {
	Topic       string
	Kind        string
	WorkspaceID string
	Data        any
}

// e2eRecorder satisfies hubemit.Publisher and stashes every emit
// under a mutex so the assertions below are race-free.
type e2eRecorder struct {
	mu     sync.Mutex
	events []e2eEvent
}

func (r *e2eRecorder) Publish(_ context.Context, topic, kind, workspaceID string, data any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e2eEvent{Topic: topic, Kind: kind, WorkspaceID: workspaceID, Data: data})
}

func (r *e2eRecorder) snapshot() []e2eEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]e2eEvent, len(r.events))
	copy(out, r.events)
	return out
}

// TestStoryStatusUpdate_E2E_HappyPath asserts the four axes that
// have to fire together when the substrate accepts the transition:
//
//	(a) HTTP 200
//	(b) one new kind:story.status_change ledger row (substrate-canonical)
//	(c) one new story.<status> WS event published
//	(d) the store row's Status reflects the requested target
func TestStoryStatusUpdate_E2E_HappyPath(t *testing.T) {
	t.Parallel()
	p, users, sessions, projects, ledgerStore, stories := newTestPortal(t, &config.Config{Env: "dev"})
	rec := &e2eRecorder{}
	stories.SetPublisher(rec)

	ctx := context.Background()
	now := time.Now().UTC()
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	proj, _ := projects.Create(ctx, user.ID, "wksp_a", "alpha", now)
	st, _ := stories.Create(ctx, story.Story{
		WorkspaceID: proj.WorkspaceID, ProjectID: proj.ID,
		Title: "happy-path", Status: story.StatusBacklog, CreatedBy: user.ID,
	}, now)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	// Capture the baseline AFTER seed so the assertion accounts for
	// the Create-time story.backlog emit (sty_1ff1065a) and the
	// substrate's own status_change row produced by the seed.
	baselineEvents := len(rec.snapshot())
	baselineRows, _ := ledgerStore.List(ctx, proj.ID, ledger.ListOptions{StoryID: st.ID, Tags: []string{"kind:story.status_change"}}, nil)

	httpRec := postStoryStatus(t, p, st.ID, sess.ID, map[string]any{"status": "ready"})
	if httpRec.Code != http.StatusOK {
		t.Fatalf("(a) HTTP status = %d, want 200; body=%s", httpRec.Code, httpRec.Body.String())
	}

	// (b) ledger row delta: exactly one new kind:story.status_change row.
	postRows, err := ledgerStore.List(ctx, proj.ID, ledger.ListOptions{StoryID: st.ID, Tags: []string{"kind:story.status_change"}}, nil)
	if err != nil {
		t.Fatalf("ledger list (post): %v", err)
	}
	if got := len(postRows) - len(baselineRows); got != 1 {
		t.Errorf("(b) kind:story.status_change row delta = %d, want 1", got)
	}

	// (c) WS event delta: exactly one new story.<status> event.
	events := rec.snapshot()
	delta := events[baselineEvents:]
	storyEvents := 0
	var lastKind string
	for _, ev := range delta {
		if len(ev.Kind) >= 6 && ev.Kind[:6] == "story." {
			storyEvents++
			lastKind = ev.Kind
		}
	}
	if storyEvents != 1 {
		t.Errorf("(c) story.* event delta = %d, want 1", storyEvents)
	}
	if lastKind != "story.ready" {
		t.Errorf("(c) story event kind = %q, want story.ready", lastKind)
	}

	// (d) store row reflects the target.
	final, err := stories.GetByID(ctx, st.ID, nil)
	if err != nil {
		t.Fatalf("store get: %v", err)
	}
	if final.Status != story.StatusReady {
		t.Errorf("(d) store Status = %q, want ready", final.Status)
	}
}

// TestStoryStatusUpdate_E2E_SadPath_IllegalTransition asserts that
// when the substrate rejects an illegal transition (done→backlog),
// NONE of the side-effects fire: 422, no new ledger row, no new
// WS event, store row unchanged. This is the axis that masked the
// silent-failure incident the story body calls out — the previous
// handler test only checked the HTTP code.
func TestStoryStatusUpdate_E2E_SadPath_IllegalTransition(t *testing.T) {
	t.Parallel()
	p, users, sessions, projects, ledgerStore, stories := newTestPortal(t, &config.Config{Env: "dev"})
	rec := &e2eRecorder{}
	stories.SetPublisher(rec)

	ctx := context.Background()
	now := time.Now().UTC()
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	proj, _ := projects.Create(ctx, user.ID, "wksp_a", "alpha", now)
	st, _ := stories.Create(ctx, story.Story{
		WorkspaceID: proj.WorkspaceID, ProjectID: proj.ID,
		Title: "terminal-target", Status: story.StatusBacklog, CreatedBy: user.ID,
	}, now)
	// Walk to done via the canonical chain so the substrate has a real
	// terminal row to reject the rollback against.
	_, _ = stories.UpdateStatus(ctx, st.ID, story.StatusReady, user.ID, now.Add(1*time.Second), nil)
	_, _ = stories.UpdateStatus(ctx, st.ID, story.StatusInProgress, user.ID, now.Add(2*time.Second), nil)
	_, _ = stories.UpdateStatus(ctx, st.ID, story.StatusDone, user.ID, now.Add(3*time.Second), nil)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	// Baselines AFTER the legal walk so the deltas are clean.
	baselineEvents := len(rec.snapshot())
	baselineRows, _ := ledgerStore.List(ctx, proj.ID, ledger.ListOptions{StoryID: st.ID, Tags: []string{"kind:story.status_change"}}, nil)

	httpRec := postStoryStatus(t, p, st.ID, sess.ID, map[string]any{"status": "backlog", "reason": "back to drawing board"})
	if httpRec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("(a) HTTP status = %d, want 422; body=%s", httpRec.Code, httpRec.Body.String())
	}

	// (b) zero new kind:story.status_change rows.
	postRows, err := ledgerStore.List(ctx, proj.ID, ledger.ListOptions{StoryID: st.ID, Tags: []string{"kind:story.status_change"}}, nil)
	if err != nil {
		t.Fatalf("ledger list (post): %v", err)
	}
	if got := len(postRows) - len(baselineRows); got != 0 {
		t.Errorf("(b) kind:story.status_change row delta = %d, want 0", got)
	}

	// (b') zero new kind:operator-override rows either — the reason
	// audit row only writes when the substrate-side mutation succeeds.
	overrideRows, _ := ledgerStore.List(ctx, proj.ID, ledger.ListOptions{StoryID: st.ID, Tags: []string{"kind:operator-override"}}, nil)
	if len(overrideRows) != 0 {
		t.Errorf("(b') kind:operator-override row count = %d, want 0 — handler must not write the audit row when the mutation is rejected", len(overrideRows))
	}

	// (c) zero new WS events.
	events := rec.snapshot()
	delta := events[baselineEvents:]
	if len(delta) != 0 {
		t.Errorf("(c) WS event delta = %d, want 0; got=%+v", len(delta), delta)
	}

	// (d) store row unchanged from done.
	final, err := stories.GetByID(ctx, st.ID, nil)
	if err != nil {
		t.Fatalf("store get: %v", err)
	}
	if final.Status != story.StatusDone {
		t.Errorf("(d) store Status = %q, want done (substrate must not have mutated)", final.Status)
	}
}
