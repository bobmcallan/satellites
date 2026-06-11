package verb

import (
	"context"
	"testing"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/project"
)

// TestEffectiveProjectRole_AgentCap pins the downscope (sty_3a1374b5): a
// requested agent-role caps the caller's actual role to min(actual, requested),
// and a project outside the allow-list resolves to "". A global admin (actual =
// admin) needs no stores, so the cap arithmetic is unit-testable directly.
func TestEffectiveProjectRole_AgentCap(t *testing.T) {
	const pid = "proj_target"
	admin := auth.WithUser(context.Background(), &auth.User{ID: "u1", Role: auth.RoleAdmin})
	cap := func(role string, projects ...string) context.Context {
		return auth.WithAgentRole(admin, auth.AgentRoleCap{Role: role, Projects: projects})
	}

	cases := []struct {
		name string
		ctx  context.Context
		want string
	}{
		{"global admin, no cap → admin", admin, project.RoleAdmin},
		{"admin capped read → read", cap("read"), project.RoleRead},
		{"admin capped write → write", cap("write"), project.RoleWrite},
		{"admin capped admin → admin (no downscope)", cap("admin"), project.RoleAdmin},
		{"capped read, project NOT in allow-list → none", cap("read", "proj_other"), ""},
		{"capped read, project in allow-list → read", cap("read", pid), project.RoleRead},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := effectiveProjectRole(c.ctx, pid); got != c.want {
				t.Fatalf("effectiveProjectRole = %q, want %q", got, c.want)
			}
		})
	}
}

// TestEffectiveProjectRole_DownscopeOnly: a cap can never escalate. A caller
// with no actual grant (nil user, no stores) requesting admin still resolves to
// "" — the cap is a ceiling, not a grant.
func TestEffectiveProjectRole_DownscopeOnly(t *testing.T) {
	ctx := auth.WithAgentRole(context.Background(), auth.AgentRoleCap{Role: project.RoleAdmin})
	if got := effectiveProjectRole(ctx, "proj_x"); got != "" {
		t.Fatalf("downscope-only: nil-user + admin cap = %q, want \"\" (no escalation)", got)
	}
}
