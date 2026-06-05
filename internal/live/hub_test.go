package live

import (
	"testing"
	"time"
)

func TestScopeAllows(t *testing.T) {
	admin := Scope{Admin: true}
	member := Scope{Workspaces: map[string]bool{"wksp_a": true}}
	none := Scope{Workspaces: map[string]bool{}}

	n := Notification{Topic: "project:proj_1", ProjectID: "proj_1", WorkspaceID: "wksp_a"}

	if !admin.Allows(n) {
		t.Fatal("admin should see every topic")
	}
	if !member.Allows(n) {
		t.Fatal("member of wksp_a should see wksp_a topic")
	}
	if none.Allows(n) {
		t.Fatal("non-member must not see a scoped topic")
	}

	// A topic with no workspace can only reach an admin.
	bare := Notification{Topic: "project:proj_1", ProjectID: "proj_1"}
	if !admin.Allows(bare) {
		t.Fatal("admin should see an unscoped topic")
	}
	if member.Allows(bare) {
		t.Fatal("non-admin must not receive a workspace-less topic")
	}
}

func TestHubPublishRoutesByScope(t *testing.T) {
	h := NewHub()
	member, cancelM := h.Subscribe(Scope{Workspaces: map[string]bool{"wksp_a": true}})
	defer cancelM()
	outsider, cancelO := h.Subscribe(Scope{Workspaces: map[string]bool{"wksp_b": true}})
	defer cancelO()

	if got := h.ClientCount(); got != 2 {
		t.Fatalf("ClientCount = %d, want 2", got)
	}

	h.Publish(Notification{Topic: "story:sty_1", ProjectID: "proj_1", WorkspaceID: "wksp_a"})

	select {
	case topic := <-member.C():
		if topic != "story:sty_1" {
			t.Fatalf("member got %q, want story:sty_1", topic)
		}
	case <-time.After(time.Second):
		t.Fatal("member did not receive an authorized topic")
	}

	select {
	case topic := <-outsider.C():
		t.Fatalf("outsider received an unauthorized topic %q", topic)
	case <-time.After(100 * time.Millisecond):
		// expected: outsider's workspace doesn't match.
	}
}

func TestHubUnsubscribeStopsDelivery(t *testing.T) {
	h := NewHub()
	c, cancel := h.Subscribe(Scope{Admin: true})
	cancel()
	if got := h.ClientCount(); got != 0 {
		t.Fatalf("ClientCount after cancel = %d, want 0", got)
	}
	cancel() // idempotent — must not panic
	h.Publish(Notification{Topic: "project:proj_1"})
	select {
	case <-c.C():
		t.Fatal("cancelled client should not receive")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestHubPublishNonBlockingOnFullBuffer(t *testing.T) {
	h := NewHub()
	c, cancel := h.Subscribe(Scope{Admin: true})
	defer cancel()

	// Overfill the buffer; Publish must never block even though nothing reads.
	done := make(chan struct{})
	go func() {
		for i := 0; i < clientBuffer*4; i++ {
			h.Publish(Notification{Topic: "project:proj_1", WorkspaceID: "wksp_a"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a full client buffer")
	}
	// The client still has buffered topics available (drops are silent).
	select {
	case <-c.C():
	case <-time.After(time.Second):
		t.Fatal("expected at least one buffered topic")
	}
}
