package portal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/document"
)

// TestConfigPage_HeadingsDropSystemPrefix covers AC1 of story_4ea61a4c
// (S9 of epic:orchestrator-driven-configuration): the four catalog
// panel titles read "contracts", "workflows", "agents", "principles"
// — without the legacy "system " prefix. Scope is conveyed by the
// per-row scope column.
func TestConfigPage_HeadingsDropSystemPrefix(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Env: "dev", DevMode: true}
	p, users, sessions, _, _, _, _, docs, _ := newTestPortalWithContracts(t, cfg)
	mux := http.NewServeMux()
	p.Register(mux)

	user := auth.User{ID: "u_headings", Email: "alice@local"}
	users.Add(user)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	now := time.Date(2026, 4, 29, 11, 0, 0, 0, time.UTC)
	for _, seed := range []struct {
		typ  string
		name string
	}{
		{document.TypeContract, "contract_x"},
		{document.TypeWorkflow, "workflow_x"},
		{document.TypeAgent, "agent_x"},
		{document.TypePrinciple, "principle_x"},
	} {
		var structured []byte
		if seed.typ == document.TypeAgent {
			structured, _ = document.MarshalAgentSettings(document.AgentSettings{
				PermissionPatterns: []string{"Read:**"},
			})
		}
		if _, err := docs.Create(context.Background(), document.Document{
			Type:       seed.typ,
			Scope:      document.ScopeSystem,
			Name:       seed.name,
			Body:       seed.name + " body",
			Status:     document.StatusActive,
			Structured: structured,
		}, now); err != nil {
			t.Fatalf("seed %s: %v", seed.name, err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()

	for _, want := range []string{">contracts<", ">workflows<", ">agents<", ">principles<"} {
		if !strings.Contains(body, want) {
			t.Errorf("expected heading %q not found", want)
		}
	}
	for _, removed := range []string{">system contracts<", ">system workflows<", ">system agents<", ">system principles<"} {
		if strings.Contains(body, removed) {
			t.Errorf("legacy prefixed heading %q should be gone", removed)
		}
	}
}

// TestConfigPage_WorkflowsAboveContracts covers AC2: the workflows
// panel renders before the contracts panel in the system section.
func TestConfigPage_WorkflowsAboveContracts(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Env: "dev", DevMode: true}
	p, users, sessions, _, _, _, _, docs, _ := newTestPortalWithContracts(t, cfg)
	mux := http.NewServeMux()
	p.Register(mux)

	user := auth.User{ID: "u_order", Email: "alice@local"}
	users.Add(user)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	now := time.Date(2026, 4, 29, 11, 0, 0, 0, time.UTC)
	if _, err := docs.Create(context.Background(), document.Document{
		Type: document.TypeContract, Scope: document.ScopeSystem,
		Name: "contract_a", Body: "c", Status: document.StatusActive,
	}, now); err != nil {
		t.Fatalf("seed contract: %v", err)
	}
	if _, err := docs.Create(context.Background(), document.Document{
		Type: document.TypeWorkflow, Scope: document.ScopeSystem,
		Name: "workflow_a", Body: "w", Status: document.StatusActive,
	}, now); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()

	wfIdx := strings.Index(body, `data-testid="config-system-workflows-panel"`)
	ctrIdx := strings.Index(body, `data-testid="config-system-contracts-panel"`)
	if wfIdx < 0 || ctrIdx < 0 {
		t.Fatalf("missing panel markers: workflows=%d contracts=%d", wfIdx, ctrIdx)
	}
	if wfIdx >= ctrIdx {
		t.Errorf("workflows panel should appear before contracts; got workflows=%d contracts=%d", wfIdx, ctrIdx)
	}
}

// TestConfigPage_CreateAffordancesPerScope covers AC3: each system
// catalog panel renders create affordances for at least one
// non-system override scope. The exact scope set varies per type
// (workflow supports workspace/project/user, agent only
// workspace/project, principle only project) but every panel must
// expose at least one create link.
func TestConfigPage_CreateAffordancesPerScope(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Env: "dev", DevMode: true}
	p, users, sessions, _, _, _, _, docs, _ := newTestPortalWithContracts(t, cfg)
	mux := http.NewServeMux()
	p.Register(mux)

	user := auth.User{ID: "u_affordances", Email: "alice@local"}
	users.Add(user)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	now := time.Date(2026, 4, 29, 11, 0, 0, 0, time.UTC)
	for _, seed := range []struct {
		typ  string
		name string
	}{
		{document.TypeContract, "contract_x"},
		{document.TypeWorkflow, "workflow_x"},
		{document.TypePrinciple, "principle_x"},
	} {
		if _, err := docs.Create(context.Background(), document.Document{
			Type:   seed.typ,
			Scope:  document.ScopeSystem,
			Name:   seed.name,
			Body:   "b",
			Status: document.StatusActive,
		}, now); err != nil {
			t.Fatalf("seed %s: %v", seed.name, err)
		}
	}
	agentSettings, _ := document.MarshalAgentSettings(document.AgentSettings{
		PermissionPatterns: []string{"Read:**"},
	})
	if _, err := docs.Create(context.Background(), document.Document{
		Type:       document.TypeAgent,
		Scope:      document.ScopeSystem,
		Name:       "agent_x",
		Body:       "agent",
		Status:     document.StatusActive,
		Structured: agentSettings,
	}, now); err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()

	for _, want := range []string{
		`data-testid="config-create-workflow-workspace"`,
		`data-testid="config-create-workflow-project"`,
		`data-testid="config-create-workflow-user"`,
		`data-testid="config-create-contract-workspace"`,
		`data-testid="config-create-contract-project"`,
		`data-testid="config-create-contract-user"`,
		`data-testid="config-create-agent-workspace"`,
		`data-testid="config-create-agent-project"`,
		`data-testid="config-create-principle-project"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected affordance marker missing: %s", want)
		}
	}
}
