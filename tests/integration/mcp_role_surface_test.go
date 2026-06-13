//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/mcpserver"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
	"github.com/mark3labs/mcp-go/mcp"
)

// recordingSession is a minimal mcp-go ClientSession that captures the
// notifications a server broadcasts, so the list_changed assertion can read
// them off a buffered channel.
type recordingSession struct {
	id          string
	ch          chan mcp.JSONRPCNotification
	initialized bool
}

func (s *recordingSession) SessionID() string { return s.id }
func (s *recordingSession) NotificationChannel() chan<- mcp.JSONRPCNotification {
	return s.ch
}
func (s *recordingSession) Initialize()       { s.initialized = true }
func (s *recordingSession) Initialized() bool { return s.initialized }

// TestMCPRoleScopedSurface is the DOGFOOD for sty_5ee95426
// (epic:workspace-admin · role-scoped-mcp-surface): the MCP tool surface is a
// function of the caller's resolved role, list-filtering is discovery only,
// and call-time enforcement is the authority.
//
//	(a) an admin's tools/list contains the workspace-admin verbs and an
//	    ordinary member's does not (both keep the base surface);
//	(b) the member invoking an admin verb directly is refused (forbidden),
//	    independent of listing;
//	(c) granting the member admin surfaces the verbs on the next list and a
//	    notifications/tools/list_changed was emitted;
//	(d) an unauthenticated caller sees the base surface only.
func TestMCPRoleScopedSurface(t *testing.T) {
	env := testbootstrap.SetUp(t)
	testbootstrap.Reset(t, env)

	ctx := context.Background()
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)

	authStore := auth.New(env.DB)
	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	docStore := document.New(env.DB)
	verb.SetAuthStore(authStore)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(pjStore)
	verb.SetDocumentStore(docStore)
	t.Cleanup(func() {
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
		verb.SetDocumentStore(nil)
		verb.SetToolsListChangedNotifier(nil)
	})

	gAdmin, _ := authStore.CreateUser(ctx, "usr_rs_gadmin", "gadmin@rs.local", "GAdmin", auth.RoleAdmin)
	member, _ := authStore.CreateUser(ctx, "usr_rs_member", "member@rs.local", "Member", auth.RoleUser)
	target, _ := authStore.CreateUser(ctx, "usr_rs_target", "target@rs.local", "Target", auth.RoleUser)

	// member belongs to the workspace as a plain member (no admin role): a
	// member, not an administrator. gAdmin owns it (→ admin member row).
	ws, _ := wsStore.Create(ctx, gAdmin.ID, "rs-ws", now)
	if err := wsStore.AddMember(ctx, ws.ID, member.ID, workspace.RoleMember, gAdmin.ID, now); err != nil {
		t.Fatalf("seed member: %v", err)
	}

	// New() binds the verb-layer list-changed notifier to THIS server's
	// broadcast, so a grant fires a notification to its registered sessions.
	s := mcpserver.New()

	const (
		adminVerb = "workspace_member_add"
		baseVerb  = "workspace_member_list"
	)

	listToolNames := func(t *testing.T, c context.Context) map[string]bool {
		t.Helper()
		resp := s.HandleMessage(c, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		raw, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("marshal tools/list resp: %v", err)
		}
		var parsed struct {
			Error  *struct{ Message string } `json:"error"`
			Result struct {
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
			} `json:"result"`
		}
		if err := json.Unmarshal(raw, &parsed); err != nil {
			t.Fatalf("unmarshal tools/list: %v (%s)", err, raw)
		}
		if parsed.Error != nil {
			t.Fatalf("tools/list error: %s", parsed.Error.Message)
		}
		names := map[string]bool{}
		for _, tl := range parsed.Result.Tools {
			names[tl.Name] = true
		}
		return names
	}

	// (a) admin sees the workspace-admin verb; member does not — both keep base.
	t.Run("list filtered by resolved role", func(t *testing.T) {
		adminTools := listToolNames(t, authWithUser(ctx, gAdmin))
		if !adminTools[adminVerb] {
			t.Fatalf("admin's tools/list missing %q; got %v", adminVerb, adminTools)
		}
		if !adminTools[baseVerb] || !adminTools["document_get"] {
			t.Fatalf("admin's tools/list missing base surface; got %v", adminTools)
		}

		memberTools := listToolNames(t, authWithUser(ctx, member))
		if memberTools[adminVerb] {
			t.Fatalf("member's tools/list must NOT contain %q; got %v", adminVerb, memberTools)
		}
		if !memberTools[baseVerb] || !memberTools["document_get"] {
			t.Fatalf("member lost the base surface; got %v", memberTools)
		}
	})

	// (b) the member calling an admin verb directly is refused — independent
	//     of listing — with valid args, so the role gate is the only reason.
	t.Run("dispatch enforced independent of listing", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{
			"name": adminVerb,
			"arguments": map[string]any{
				"workspace_id": ws.ID,
				"user_id":      target.ID,
				"role":         "member",
			},
		})
		call := func(c context.Context) (bool, string) {
			req, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0", "id": 2, "method": "tools/call",
				"params": json.RawMessage(args),
			})
			resp := s.HandleMessage(c, req)
			raw, _ := json.Marshal(resp)
			var parsed struct {
				Result struct {
					IsError bool `json:"isError"`
					Content []struct {
						Text string `json:"text"`
					} `json:"content"`
				} `json:"result"`
			}
			_ = json.Unmarshal(raw, &parsed)
			txt := ""
			if len(parsed.Result.Content) > 0 {
				txt = parsed.Result.Content[0].Text
			}
			return parsed.Result.IsError, txt
		}

		if isErr, txt := call(authWithUser(ctx, member)); !isErr || !strings.Contains(txt, "forbidden") {
			t.Fatalf("member call of %q should be forbidden; isError=%v text=%q", adminVerb, isErr, txt)
		}
		// The admin's identical call passes the gate (and the verb).
		if isErr, txt := call(authWithUser(ctx, gAdmin)); isErr {
			t.Fatalf("admin call of %q should succeed; text=%q", adminVerb, txt)
		}
	})

	// (c) grant the member admin → next list surfaces the verb, and a
	//     notifications/tools/list_changed was broadcast to the session.
	t.Run("grant surfaces verbs and emits list_changed", func(t *testing.T) {
		sess := &recordingSession{id: "sess-rs-1", ch: make(chan mcp.JSONRPCNotification, 8)}
		if err := s.RegisterSession(ctx, sess); err != nil {
			t.Fatalf("register session: %v", err)
		}
		sess.Initialize()
		t.Cleanup(func() { s.UnregisterSession(ctx, sess.id) })

		// Before: the member cannot see the admin verb.
		if listToolNames(t, authWithUser(ctx, member))[adminVerb] {
			t.Fatalf("precondition: member should not yet see %q", adminVerb)
		}

		// Admin grants the member workspace 'admin' via the real verb path.
		if _, err := verb.Dispatch(authWithUser(ctx, gAdmin), "workspace_member_update_role", json.RawMessage(
			`{"workspace_id":"`+ws.ID+`","user_id":"`+member.ID+`","role":"admin"}`)); err != nil {
			t.Fatalf("grant admin: %v", err)
		}

		// A list_changed notification reached the registered session.
		select {
		case n := <-sess.ch:
			if n.Method != mcp.MethodNotificationToolsListChanged {
				t.Fatalf("unexpected notification %q, want %q", n.Method, mcp.MethodNotificationToolsListChanged)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("no tools/list_changed notification after grant")
		}

		// After: re-listing as the (now-admin) member surfaces the verb.
		if !listToolNames(t, authWithUser(ctx, member))[adminVerb] {
			t.Fatalf("after grant, member's tools/list still missing %q", adminVerb)
		}
	})

	// (d) the filter degrades safely: an unauthenticated caller sees the base
	//     surface only and never the workspace-admin verbs.
	t.Run("unauthenticated sees base surface only", func(t *testing.T) {
		anon := listToolNames(t, ctx) // no auth.User on ctx
		if anon[adminVerb] {
			t.Fatalf("unauthenticated caller must not see %q; got %v", adminVerb, anon)
		}
		if !anon["document_get"] || !anon[baseVerb] {
			t.Fatalf("unauthenticated caller lost the base surface; got %v", anon)
		}
	})
}
