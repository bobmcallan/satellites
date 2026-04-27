// Tests for the /config page extensions shipped in story_7b77ffb0:
// agents section + per-phase allocation panel.
package portal

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/story"
)

// TestConfig_AgentsSection verifies AC4 (agents section). The /config
// page lists every type=agent document the caller can see, even when
// no Configuration is selected.
func TestConfig_AgentsSection(t *testing.T) {
	t.Parallel()
	p, users, sessions, _, _, _, _, docs, _ := newTestPortalWithContracts(t, &config.Config{Env: "dev"})
	ctx := context.Background()
	now := time.Now().UTC()
	user := auth.User{ID: "u_1", Email: "alice@local"}
	users.Add(user)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	seedAgent(t, docs, "preplan_agent", document.AgentSettings{
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
	if !strings.Contains(body, "preplan_agent") {
		t.Errorf("seeded agent name not in /config body; body=%s", body)
	}
	_ = ctx
}

// TestConfig_PerPhaseAllocation verifies AC4 (per-phase allocation).
// A story with configuration_id=<configID> and a CI bound to a
// contract in the configuration's ContractRefs surfaces in the
// allocation table.
func TestConfig_PerPhaseAllocation(t *testing.T) {
	t.Parallel()
	p, users, sessions, _, _, stories, contracts, docs, _ := newTestPortalWithContracts(t, &config.Config{Env: "dev"})
	ctx := context.Background()
	now := time.Now().UTC()
	user := auth.User{ID: "u_1", Email: "alice@local"}
	users.Add(user)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	contractDoc := seedDoc(t, docs, "", document.TypeContract, "develop", "body", now)
	cfgRefs := document.Configuration{
		ContractRefs: []string{contractDoc.ID},
	}
	cfgDoc := seedConfiguration(t, docs, "proj_x", "primary", cfgRefs, now)
	agent := seedAgent(t, docs, "develop_agent", document.AgentSettings{
		PermissionPatterns: []string{"Edit:**"},
	}, "", now)

	configID := cfgDoc.ID
	s, err := stories.Create(ctx, story.Story{
		ProjectID: "proj_x", Title: "alloc-test", Status: "in_progress",
		Priority: "high", Category: "feature", CreatedBy: user.ID,
		ConfigurationID: &configID,
	}, now)
	if err != nil {
		t.Fatalf("seed story: %v", err)
	}
	if _, err := contracts.Create(ctx, contract.ContractInstance{
		StoryID: s.ID, ContractID: contractDoc.ID, ContractName: "develop",
		Sequence: 1, Status: contract.StatusClaimed, AgentID: agent.ID,
	}, now); err != nil {
		t.Fatalf("seed CI: %v", err)
	}

	rec := renderPath(t, p, "/config?id="+cfgDoc.ID, sess.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-testid="config-allocation-panel"`) {
		t.Errorf("config-allocation-panel missing; body=%s", body)
	}
	if !strings.Contains(body, "develop_agent") {
		t.Errorf("allocation panel missing agent name; body=%s", body)
	}
	if !strings.Contains(body, s.ID) {
		t.Errorf("allocation panel missing story id; body=%s", body)
	}
}
