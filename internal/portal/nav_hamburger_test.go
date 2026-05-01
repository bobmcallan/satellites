// Regression guard for sty_7e53a4e3 — the hamburger dropdown stayed
// hidden after click because `@click.outside="close"` lived on the
// `.nav-dropdown` element, while the trigger button was its sibling.
// Clicking the button fired both `toggle` (button @click) AND `close`
// (the dropdown's outside handler, since the button is "outside" the
// dropdown). Fix: move @click.outside to the wrapping
// .nav-hamburger-wrap div which contains both the button and the
// dropdown.
//
// Same bug shape was present on the workspace menu (.nav-workspace
// wrapper). Both are asserted here so neither regresses independently.
package portal

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/pages"
)

func TestNav_HamburgerOutsideHandlerOnWrapperNotDropdown(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Env: "dev", DevMode: true}
	p, users, sessions, _, _, _ := newTestPortal(t, cfg)
	mux := http.NewServeMux()
	p.Register(mux)

	user := auth.User{ID: "u_1", Email: "alice@local"}
	users.Add(user)
	sess, _ := sessions.Create(user.ID, auth.DefaultSessionTTL)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	body := rec.Body.String()

	// New wiring: @click.outside lives on the wrapper, not the dropdown.
	if !strings.Contains(body, `<div class="nav-hamburger-wrap" x-data="navHamburger" @keydown.escape.window="close" @click.outside="close">`) {
		t.Errorf("nav-hamburger-wrap missing @click.outside binding (the fix for sty_7e53a4e3 must keep it on the wrapper)")
	}
	// Old wiring (the bug) is gone — outside handler is no longer on the
	// dropdown div itself.
	if strings.Contains(body, `data-testid="nav-dropdown" @click.outside="close"`) {
		t.Errorf("nav-dropdown still carries @click.outside; the bug from sty_7e53a4e3 has regressed")
	}
}

// TestNav_WorkspaceMenuOutsideHandlerOnWrapperNotMenu is a template-source
// regression — the workspace menu wrapper uses the same toggle-then-close
// pattern as the hamburger and would suffer the same bug if @click.outside
// drifted onto the inner <ul>. We assert directly against the parsed
// nav.html template rather than spinning up a live workspace fixture
// (the test helper's workspace store is nil in pre-tenant mode).
func TestNav_WorkspaceMenuOutsideHandlerOnWrapperNotMenu(t *testing.T) {
	t.Parallel()
	tmpls, err := pages.Templates()
	if err != nil {
		t.Fatalf("pages.Templates: %v", err)
	}
	src := tmpls.Lookup("nav.html").Tree.Root.String()
	if !strings.Contains(src, `<div class="nav-workspace" x-data="navWorkspaceMenu" @keydown.escape.window="close" @click.outside="close">`) {
		t.Errorf("nav.html source: nav-workspace wrapper missing @click.outside (sty_7e53a4e3 fix)")
	}
	if strings.Contains(src, `data-testid="nav-workspace-menu" @click.outside="close"`) {
		t.Errorf("nav.html source: nav-workspace-menu still carries @click.outside on the inner ul; bug shape regressed")
	}
}
