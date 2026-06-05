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

func TestHubStats(t *testing.T) {
	h := NewHub()
	if s := h.Stats(); s.Clients != 0 || s.Published != 0 || s.Delivered != 0 {
		t.Fatalf("fresh hub stats not zero: %+v", s)
	}

	c, cancel := h.Subscribe(Scope{Admin: true})
	defer cancel()

	h.Publish(Notification{Topic: "story:sty_1", WorkspaceID: "wksp_a"})
	<-c.C() // drain so the next publish also delivers

	s := h.Stats()
	if s.Clients != 1 {
		t.Fatalf("Clients = %d, want 1", s.Clients)
	}
	if s.Published != 1 {
		t.Fatalf("Published = %d, want 1", s.Published)
	}
	if s.Delivered != 1 {
		t.Fatalf("Delivered = %d, want 1", s.Delivered)
	}
	if s.LastTopic != "story:sty_1" {
		t.Fatalf("LastTopic = %q, want story:sty_1", s.LastTopic)
	}
	if s.LastUnixNano == 0 {
		t.Fatal("LastUnixNano should be set after a publish")
	}

	// Overfill the buffer without reading → drops are counted.
	for i := 0; i < clientBuffer*2; i++ {
		h.Publish(Notification{Topic: "story:sty_1", WorkspaceID: "wksp_a"})
	}
	if h.Stats().Dropped == 0 {
		t.Fatal("expected Dropped > 0 after overfilling a client buffer")
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
