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

// TestConfigPage_MandateStackPanel covers AC4 of story_f0a78759: the
// /config page renders an "active mandate stack" panel listing each
// scope tier's workflow markdowns + the merged effective list. The
// test seeds a system workflow Document and asserts the panel header,
// the system layer's slot, and the effective table are present.
func TestConfigPage_MandateStackPanel(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Env: "dev", DevMode: true}
	p, users, sessions, _, _, _, _, docs, _ := newTestPortalWithContracts(t, cfg)
	mux := http.NewServeMux()
	p.Register(mux)

	user := auth.User{ID: "u_mandate", Email: "alice@local"}
	users.Add(user)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	now := time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC)

	systemSrc := document.Document{
		Type:   document.TypeWorkflow,
		Scope:  document.ScopeSystem,
		Name:   "default",
		Body:   "system workflow body",
		Status: document.StatusActive,
		Structured: []byte(`{"required_slots":[
			{"contract_name":"preplan","required":true,"min_count":1,"max_count":1},
			{"contract_name":"plan","required":true,"min_count":1,"max_count":1}
		]}`),
	}
	if _, err := docs.Create(context.Background(), systemSrc, now); err != nil {
		t.Fatalf("seed system workflow: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()

	// Panel header present.
	if !strings.Contains(body, `data-testid="config-mandate-stack-panel"`) {
		t.Fatalf("mandate-stack panel not rendered; body=%s", body)
	}
	if !strings.Contains(body, "active mandate stack") {
		t.Fatalf("active mandate stack header missing; body=%s", body)
	}

	// Effective table contains the system slot.
	if !strings.Contains(body, `data-testid="config-mandate-slot-preplan"`) {
		t.Fatalf("effective slot row for preplan missing; body=%s", body)
	}
	if !strings.Contains(body, `data-testid="config-mandate-slot-plan"`) {
		t.Fatalf("effective slot row for plan missing; body=%s", body)
	}
	// System layer panel rendered with the workflow name.
	if !strings.Contains(body, "default") {
		t.Fatalf("system workflow name 'default' missing from layer panel; body=%s", body)
	}
}

// TestConfigPage_MandateStack_EmptyTiers verifies the panel still
// renders cleanly when no workflow documents exist at any tier.
// The empty-state row replaces the effective table.
func TestConfigPage_MandateStack_EmptyTiers(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Env: "dev", DevMode: true}
	p, users, sessions, _, _, _, _, _, _ := newTestPortalWithContracts(t, cfg)
	mux := http.NewServeMux()
	p.Register(mux)

	user := auth.User{ID: "u_empty", Email: "alice@local"}
	users.Add(user)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()

	if !strings.Contains(body, `data-testid="config-mandate-stack-panel"`) {
		t.Fatalf("mandate-stack panel missing in empty-state; body=%s", body)
	}
	if !strings.Contains(body, `data-testid="config-mandate-empty"`) {
		t.Fatalf("empty-state marker missing; body=%s", body)
	}
}
