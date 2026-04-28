package mcpserver

import (
	"encoding/json"
	"testing"
)

// TestSessionWhoami_EchoesWorkspaceID covers AC1 + AC6 of story_798631fd:
// after session_register binds a workspace_id, session_whoami echoes it
// back in the response payload.
func TestSessionWhoami_EchoesWorkspaceID(t *testing.T) {
	t.Parallel()
	f := newClaimFixture(t)

	// Re-register the fixture session with a workspace_id; existing
	// fixture session was registered without one.
	res, err := f.server.handleSessionRegister(f.callerCtx(), newCallToolReq("session_register", map[string]any{
		"session_id":   f.sessionID,
		"workspace_id": "wksp_alpha",
	}))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if res.IsError {
		t.Fatalf("register isError: %s", firstText(res))
	}

	res, err = f.server.handleSessionWhoami(f.callerCtx(), newCallToolReq("session_whoami", map[string]any{
		"session_id": f.sessionID,
	}))
	if err != nil {
		t.Fatalf("whoami: %v", err)
	}
	if res.IsError {
		t.Fatalf("whoami isError: %s", firstText(res))
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(firstText(res)), &payload); err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, _ := payload["workspace_id"].(string)
	if got != "wksp_alpha" {
		t.Errorf("whoami workspace_id = %q, want %q (full payload: %v)", got, "wksp_alpha", payload)
	}
}

// TestSessionWhoami_OmitsWorkspaceIDWhenUnbound covers the negative
// case for AC1: when no workspace has been bound, the field is absent
// from the response (omitempty).
func TestSessionWhoami_OmitsWorkspaceIDWhenUnbound(t *testing.T) {
	t.Parallel()
	f := newClaimFixture(t)

	// Fixture session was registered without workspace_id.
	res, err := f.server.handleSessionWhoami(f.callerCtx(), newCallToolReq("session_whoami", map[string]any{
		"session_id": f.sessionID,
	}))
	if err != nil {
		t.Fatalf("whoami: %v", err)
	}
	if res.IsError {
		t.Fatalf("whoami isError: %s", firstText(res))
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(firstText(res)), &payload); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, ok := payload["workspace_id"]; ok {
		t.Errorf("whoami payload contains workspace_id when none was bound; payload = %v", payload)
	}
}
