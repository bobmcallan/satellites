//go:build integration

package integration_test

import (
	"bufio"
	"context"
	"net/http"
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

// TestLiveEventsBus exercises the SSE trigger bus end-to-end (sty_b6e39eb8):
// a real Postgres NOTIFY (fired by the evidence INSERT trigger via a ledger
// append) flows through pq LISTEN → hub → the session-gated GET /events stream
// to an authorized client, is withheld from an unauthorized one, and resumes
// on reconnect.
func TestLiveEventsBus(t *testing.T) {
	env := testbootstrap.SetUp(t)
	ctx := context.Background()
	now := time.Now().UTC()

	authStore := auth.New(env.DB)
	wsStore := workspace.New(env.DB)
	ledgerStore := ledger.New(env.DB)

	// A workspace, a member of it, and an outsider who is not.
	ws, err := wsStore.Create(ctx, "", "live-test-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	member, err := authStore.CreateUser(ctx, "usr_live_member", "member@live.test", "Member", auth.RoleUser)
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	outsider, err := authStore.CreateUser(ctx, "usr_live_outsider", "outsider@live.test", "Outsider", auth.RoleUser)
	if err != nil {
		t.Fatalf("create outsider: %v", err)
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
		if u, err := authStore.GetUserByID(sctx, userID); err == nil && u != nil && u.Role == auth.RoleAdmin {
			return live.Scope{Admin: true}, nil
		}
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

	const projectID = "proj_live_test"
	const storyID = "sty_live_test"

	// fire appends one evidence row scoped to (storyID, projectID, ws.ID),
	// which commits and triggers a NOTIFY for story:<id> and project:<id>.
	fire := func(t *testing.T) {
		t.Helper()
		if _, err := ledgerStore.Append(ctx, ledger.AppendInput{
			StoryID:     storyID,
			ProjectID:   projectID,
			WorkspaceID: ws.ID,
			Kind:        "test_trigger",
			Actor:       "test",
			Body:        "live bus probe",
		}, time.Now().UTC()); err != nil {
			t.Fatalf("ledger append: %v", err)
		}
	}

	// member sees its workspace's topics.
	t.Run("authorized member receives the trigger", func(t *testing.T) {
		topics, stop := openStream(t, srv.URL, sessions, member.ID)
		defer stop()
		fire(t)
		got := awaitTopic(t, topics, 4*time.Second)
		if !strings.HasPrefix(got, "story:") && !strings.HasPrefix(got, "project:") {
			t.Fatalf("unexpected topic %q", got)
		}
	})

	// outsider (not a member, not admin) must not receive a workspace-scoped
	// topic — AC3 / AC6 negative.
	t.Run("unauthorized user receives nothing", func(t *testing.T) {
		topics, stop := openStream(t, srv.URL, sessions, outsider.ID)
		defer stop()
		fire(t)
		if got, ok := tryTopic(topics, 1500*time.Millisecond); ok {
			t.Fatalf("outsider received an unauthorized topic %q", got)
		}
	})

	// reconnect: a fresh connection picks up a subsequently-fired trigger.
	t.Run("reconnect resumes delivery", func(t *testing.T) {
		topics1, stop1 := openStream(t, srv.URL, sessions, member.ID)
		stop1() // drop the first connection

		topics2, stop2 := openStream(t, srv.URL, sessions, member.ID)
		defer stop2()
		fire(t)
		_ = awaitTopic(t, topics2, 4*time.Second)
		_ = topics1
	})
}

// openStream opens an authenticated SSE connection to /events for userID and
// returns a channel of delivered topic strings plus a stop func. It blocks
// until the server's ": connected" preamble arrives, guaranteeing the client
// is subscribed before the caller fires a trigger.
func openStream(t *testing.T, baseURL string, sessions *auth.Sessions, userID string) (<-chan string, func()) {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, baseURL+"/events", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	rec := httptest.NewRecorder()
	sessions.Issue(rec, userID)
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}
	reqCtx, cancel := context.WithCancel(context.Background())
	req = req.WithContext(reqCtx)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("open stream: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		cancel()
		resp.Body.Close()
		t.Fatalf("stream status = %d, want 200", resp.StatusCode)
	}

	topics := make(chan string, 16)
	connected := make(chan struct{})
	go func() {
		defer close(topics)
		sc := bufio.NewScanner(resp.Body)
		var sawConnected bool
		for sc.Scan() {
			line := sc.Text()
			if !sawConnected && strings.Contains(line, "connected") {
				sawConnected = true
				close(connected)
				continue
			}
			if data, ok := strings.CutPrefix(line, "data: "); ok {
				topics <- data
			}
		}
	}()

	select {
	case <-connected:
	case <-time.After(3 * time.Second):
		cancel()
		resp.Body.Close()
		t.Fatal("did not receive ': connected' preamble")
	}

	stop := func() {
		cancel()
		resp.Body.Close()
	}
	return topics, stop
}

// awaitTopic returns the next delivered topic or fails after d.
func awaitTopic(t *testing.T, topics <-chan string, d time.Duration) string {
	t.Helper()
	got, ok := tryTopic(topics, d)
	if !ok {
		t.Fatal("timed out waiting for a topic")
	}
	return got
}

// tryTopic waits up to d for a topic; ok=false on timeout (used to assert
// absence).
func tryTopic(topics <-chan string, d time.Duration) (string, bool) {
	select {
	case topic, ok := <-topics:
		if !ok {
			return "", false
		}
		return topic, true
	case <-time.After(d):
		return "", false
	}
}
