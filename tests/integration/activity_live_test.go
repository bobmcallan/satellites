//go:build integration

package integration_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/live"
	"github.com/bobmcallan/satellites/internal/server"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestActivityTopic dogfoods migration 0030 (epic:dynamic-workflow-status,
// order:1): an engagement:* ledger append fires a dedicated activity:<story> SSE
// topic — through real Postgres NOTIFY → pq LISTEN → hub → the session-gated
// /events stream — while a non-engagement append fires none. This is the
// real-time "alive" signal the portal's activity indicator consumes.
func TestActivityTopic(t *testing.T) {
	env := testbootstrap.SetUp(t)
	ctx := context.Background()
	now := time.Now().UTC()

	authStore := auth.New(env.DB)
	wsStore := workspace.New(env.DB)
	ledgerStore := ledger.New(env.DB)

	ws, err := wsStore.Create(ctx, "", "activity-test-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	member, err := authStore.CreateUser(ctx, "usr_act_member", "member@act.test", "Member", auth.RoleUser)
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	if err := wsStore.AddMember(ctx, ws.ID, member.ID, workspace.RoleMember, "", now); err != nil {
		t.Fatalf("add member: %v", err)
	}

	sessions := auth.NewSessions([]byte("0123456789abcdef0123456789abcdef"))
	hub := live.NewHub()
	listener, err := live.NewListener(env.DSN, hub)
	if err != nil {
		t.Fatalf("start listener: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	liveScope := func(sctx context.Context, userID string) (live.Scope, error) {
		ids, err := wsStore.ListWorkspaceIDsForUser(sctx, userID)
		if err != nil {
			return live.Scope{}, err
		}
		return live.NewScope(false, ids), nil
	}
	handler := server.Build(server.Config{
		Store:     authStore,
		Sessions:  sessions,
		LiveScope: liveScope,
		Live:      hub,
		DevMode:   true,
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	const projectID = "proj_act_test"
	const storyID = "sty_act_test"

	appendRow := func(t *testing.T, kind string) {
		t.Helper()
		if _, err := ledgerStore.Append(ctx, ledger.AppendInput{
			StoryID:     storyID,
			ProjectID:   projectID,
			WorkspaceID: ws.ID,
			Kind:        kind,
			Actor:       "test",
			Body:        "activity probe",
		}, time.Now().UTC()); err != nil {
			t.Fatalf("ledger append %s: %v", kind, err)
		}
	}

	t.Run("engagement row fires the activity topic", func(t *testing.T) {
		topics, stop := openStream(t, srv.URL, sessions, member.ID)
		defer stop()
		appendRow(t, "engagement:engage")
		if !awaitTopicMatch(topics, "activity:"+storyID, 4*time.Second) {
			t.Fatalf("did not receive activity:%s topic", storyID)
		}
	})

	t.Run("non-engagement row fires no activity topic", func(t *testing.T) {
		topics, stop := openStream(t, srv.URL, sessions, member.ID)
		defer stop()
		appendRow(t, "status_transition")
		// story:/project: are expected; an activity: topic for a non-engagement
		// row would be a defect. Drain briefly and assert none appears.
		deadline := time.After(2 * time.Second)
		for {
			select {
			case topic, ok := <-topics:
				if !ok {
					return
				}
				if strings.HasPrefix(topic, "activity:") {
					t.Fatalf("unexpected activity topic %q for a non-engagement row", topic)
				}
			case <-deadline:
				return
			}
		}
	})
}

// awaitTopicMatch drains topics until `want` arrives or the deadline lapses.
func awaitTopicMatch(topics <-chan string, want string, d time.Duration) bool {
	deadline := time.After(d)
	for {
		select {
		case topic, ok := <-topics:
			if !ok {
				return false
			}
			if topic == want {
				return true
			}
		case <-deadline:
			return false
		}
	}
}
