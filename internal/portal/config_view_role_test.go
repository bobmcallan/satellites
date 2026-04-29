// Tests for /config agents-section rendering.
package portal

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/document"
)

// TestConfig_AgentsSection — the /config page lists every type=agent
// document the caller can see.
func TestConfig_AgentsSection(t *testing.T) {
	t.Parallel()
	p, users, sessions, _, _, _, _, docs, _ := newTestPortalWithContracts(t, &config.Config{Env: "dev"})
	ctx := context.Background()
	now := time.Now().UTC()
	user := auth.User{ID: "u_1", Email: "alice@local"}
	users.Add(user)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	seedAgent(t, docs, "developer_agent", document.AgentSettings{
		PermissionPatterns: []string{"Read:**"},
	}, "", now)

	rec := renderPath(t, p, "/config", sess.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-testid="config-agents-panel"`) {
		t.Errorf("config-agents-panel missing")
	}
	if !strings.Contains(body, "developer_agent") {
		t.Errorf("seeded agent name not in /config body; body=%s", body)
	}
	_ = ctx
}
