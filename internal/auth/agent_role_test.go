package auth

import (
	"context"
	"net/http/httptest"
	"testing"
)

// TestAgentRoleFromHeader pins the header carrier (sty_3a1374b5): a recognised
// X-Satellites-Agent-Role (+ optional X-Satellites-Agent-Projects) becomes a
// cap on ctx; an absent or unrecognised role leaves ctx capless (full user).
func TestAgentRoleFromHeader(t *testing.T) {
	mk := func(role, projects string) context.Context {
		r := httptest.NewRequest("POST", "/mcp", nil)
		if role != "" {
			r.Header.Set("X-Satellites-Agent-Role", role)
		}
		if projects != "" {
			r.Header.Set("X-Satellites-Agent-Projects", projects)
		}
		return agentRoleFromHeader(context.Background(), r)
	}

	cap, ok := AgentRoleFromContext(mk("read", "proj_a, proj_b"))
	if !ok || cap.Role != "read" || len(cap.Projects) != 2 || cap.Projects[0] != "proj_a" || cap.Projects[1] != "proj_b" {
		t.Fatalf("cap = %+v ok=%v, want read/[proj_a proj_b]", cap, ok)
	}

	if _, ok := AgentRoleFromContext(mk("", "")); ok {
		t.Error("no header → no cap")
	}
	if _, ok := AgentRoleFromContext(mk("superuser", "")); ok {
		t.Error("unrecognised role → no cap")
	}
	if _, ok := AgentRoleFromContext(mk("WRITE", "")); !ok {
		t.Error("role should be case-insensitive (WRITE)")
	}
}
