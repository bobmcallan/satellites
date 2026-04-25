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
	"github.com/bobmcallan/satellites/internal/rolegrant"
	"github.com/bobmcallan/satellites/internal/workspace"
)

func renderPath(t *testing.T, p *Portal, path, sessionCookie string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	p.Register(mux)
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if sessionCookie != "" {
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sessionCookie})
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestRolesPage_EmptyState(t *testing.T) {
	t.Parallel()
	p, users, sessions, _, _, _ := newTestPortal(t, &config.Config{Env: "dev"})
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)
	rec := renderPath(t, p, "/roles", sess.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `data-testid="roles-empty"`) {
		t.Errorf("expected roles-empty marker for no-roles state")
	}
}

func TestRolesPage_PopulatedAndCountsActiveGrants(t *testing.T) {
	t.Parallel()
	p, users, sessions, _, _, _ := newTestPortal(t, &config.Config{Env: "dev"})
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	ctx := context.Background()
	now := time.Now().UTC()
	role, err := p.documents.(*document.MemoryStore).Create(ctx, document.Document{
		Type: "role", Scope: "system", Name: "role_orchestrator", Status: "active",
		Body: "orchestrator role body",
	}, now)
	if err != nil {
		t.Fatalf("seed role: %v", err)
	}
	agent, err := p.documents.(*document.MemoryStore).Create(ctx, document.Document{
		Type: "agent", Scope: "system", Name: "agent_orchestrator", Status: "active",
		Body: "orchestrator agent body",
	}, now)
	if err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	wsID := "ws_test"
	if _, err := p.grants.(*rolegrant.MemoryStore).Create(ctx, rolegrant.RoleGrant{
		WorkspaceID: wsID,
		RoleID:      role.ID,
		AgentID:     agent.ID,
		GranteeKind: "session",
		GranteeID:   "sess_x",
	}, now); err != nil {
		t.Fatalf("seed grant: %v", err)
	}

	rec := renderPath(t, p, "/roles", sess.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "role_orchestrator") {
		t.Errorf("roles page missing seeded role name")
	}
	// Active grant count >= 1
	if !strings.Contains(body, ">1<") {
		t.Errorf("roles page missing active-grant count of 1; body=%s", body)
	}
}

func TestAgentsPage_RendersAgents(t *testing.T) {
	t.Parallel()
	p, users, sessions, _, _, _ := newTestPortal(t, &config.Config{Env: "dev"})
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := p.documents.(*document.MemoryStore).Create(ctx, document.Document{
		Type: "agent", Scope: "system", Name: "agent_orchestrator", Status: "active",
		Body: "agent body",
	}, now); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	rec := renderPath(t, p, "/agents", sess.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "agent_orchestrator") {
		t.Errorf("agents page missing seeded agent")
	}
}

func TestGrantsPage_NoAdminShowsDisabledRevoke(t *testing.T) {
	t.Parallel()
	p, users, sessions, _, _, _ := newTestPortal(t, &config.Config{Env: "dev"})
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)
	ctx := context.Background()
	now := time.Now().UTC()
	role, _ := p.documents.(*document.MemoryStore).Create(ctx, document.Document{
		Type: "role", Scope: "system", Name: "role_x", Status: "active", Body: "b",
	}, now)
	agent, _ := p.documents.(*document.MemoryStore).Create(ctx, document.Document{
		Type: "agent", Scope: "system", Name: "agent_x", Status: "active", Body: "b",
	}, now)
	if _, err := p.grants.(*rolegrant.MemoryStore).Create(ctx, rolegrant.RoleGrant{
		WorkspaceID: "ws_test", RoleID: role.ID, AgentID: agent.ID,
		GranteeKind: "session", GranteeID: "sess_y",
	}, now); err != nil {
		t.Fatalf("seed grant: %v", err)
	}
	rec := renderPath(t, p, "/grants", sess.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// SSR row appears with the seeded grant; "JS required to revoke"
	// is the noscript copy for non-JS users.
	if !strings.Contains(body, "role_x") || !strings.Contains(body, "agent_x") {
		t.Errorf("grants page missing seeded grant content")
	}
	if !strings.Contains(body, "JS required to revoke") {
		t.Errorf("grants page missing noscript revoke notice")
	}
}

func TestGrantsRelease_NonAdmin403(t *testing.T) {
	t.Parallel()
	p, users, sessions, _ := newPortalWithWorkspace(t, &config.Config{Env: "dev"})
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	mux := http.NewServeMux()
	p.Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/grants/grant_xyz/release", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for non-admin", rec.Code)
	}
}

func TestGrantsRelease_AdminReleases(t *testing.T) {
	t.Parallel()
	p, users, sessions, ws := newPortalWithWorkspace(t, &config.Config{Env: "dev"})
	user := auth.User{ID: "u_alice", Email: "alice@local"}
	users.Add(user)
	ctx := context.Background()
	now := time.Now().UTC()
	wsRow, err := ws.Create(ctx, user.ID, "alpha-ws", now)
	if err != nil {
		t.Fatalf("seed ws: %v", err)
	}
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)
	_ = sessions.SetActiveWorkspace(sess.ID, wsRow.ID)
	// Confirm GetRole reports admin (the workspace store auto-adds the
	// owner as admin on Create).
	if r, _ := ws.GetRole(ctx, wsRow.ID, user.ID); r != workspace.RoleAdmin {
		t.Fatalf("expected RoleAdmin on owner; got %q", r)
	}

	role, _ := p.documents.(*document.MemoryStore).Create(ctx, document.Document{
		Type: "role", Scope: "system", Name: "role_x", Status: "active", Body: "b",
	}, now)
	agent, _ := p.documents.(*document.MemoryStore).Create(ctx, document.Document{
		Type: "agent", Scope: "system", Name: "agent_x", Status: "active", Body: "b",
	}, now)
	g, err := p.grants.(*rolegrant.MemoryStore).Create(ctx, rolegrant.RoleGrant{
		WorkspaceID: wsRow.ID,
		RoleID:      role.ID,
		AgentID:     agent.ID,
		GranteeKind: "session",
		GranteeID:   "sess_z",
	}, now)
	if err != nil {
		t.Fatalf("seed grant: %v", err)
	}

	mux := http.NewServeMux()
	p.Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/grants/"+g.ID+"/release", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	// The grant should now be released.
	updated, err := p.grants.(*rolegrant.MemoryStore).GetByID(ctx, g.ID, []string{wsRow.ID})
	if err != nil {
		t.Fatalf("get released: %v", err)
	}
	if updated.Status != rolegrant.StatusReleased {
		t.Errorf("grant status = %q, want %q", updated.Status, rolegrant.StatusReleased)
	}
}
