package portal

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/config"
)

// adminFixture builds a portal where alice is the global_admin (via env
// list) and bob is a regular user. story_33e1a323.
type adminFixture struct {
	portal   *Portal
	mux      *http.ServeMux
	users    *auth.MemoryUserStore
	sessions *auth.MemorySessionStore
}

func newAdminFixture(t *testing.T, adminEmail string) *adminFixture {
	t.Helper()
	t.Setenv(auth.GlobalAdminEmailsEnv, adminEmail)
	p, users, sessions, _, _, _, _, _ := newTestPortalWithContracts(t, &config.Config{Env: "dev"})
	mux := http.NewServeMux()
	p.Register(mux)
	return &adminFixture{portal: p, mux: mux, users: users, sessions: sessions}
}

func (f *adminFixture) signIn(t *testing.T, email string) string {
	t.Helper()
	user := auth.User{ID: "u_" + email, Email: email}
	f.users.Add(user)
	sess, _ := f.sessions.Create(user.ID, auth.DefaultSessionTTL)
	return sess.ID
}

// TestAdminSystemConfig_404ForNonAdmin covers AC4: GET /admin/system-config
// returns 404 for non-admins to avoid leaking page existence.
func TestAdminSystemConfig_404ForNonAdmin(t *testing.T) {
	f := newAdminFixture(t, "alice@x.io")
	sessID := f.signIn(t, "bob@x.io")

	req := httptest.NewRequest(http.MethodGet, "/admin/system-config", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sessID})
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("non-admin GET status = %d, want 404", rec.Code)
	}
}

// TestAdminSystemConfig_RendersForAdmin covers AC4: GET returns 200 +
// renders the admin panel for global_admin sessions.
func TestAdminSystemConfig_RendersForAdmin(t *testing.T) {
	f := newAdminFixture(t, "alice@x.io")
	sessID := f.signIn(t, "alice@x.io")

	req := httptest.NewRequest(http.MethodGet, "/admin/system-config", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sessID})
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("admin GET status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`data-testid="admin-system-config-panel"`,
		`data-testid="admin-reseed-form"`,
		`data-testid="admin-reseed-button"`,
		`data-testid="admin-no-runs"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

// TestAdminSystemConfigReseed_404ForNonAdmin covers AC5: non-admin
// POST is denied via 404.
func TestAdminSystemConfigReseed_404ForNonAdmin(t *testing.T) {
	f := newAdminFixture(t, "alice@x.io")
	sessID := f.signIn(t, "bob@x.io")

	req := httptest.NewRequest(http.MethodPost, "/admin/system-config/reseed", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sessID})
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("non-admin POST status = %d, want 404", rec.Code)
	}
}

// TestAdminSystemConfigReseed_AdminRedirects covers AC5: an admin POST
// redirects back to the admin page (303).
func TestAdminSystemConfigReseed_AdminRedirects(t *testing.T) {
	f := newAdminFixture(t, "alice@x.io")
	sessID := f.signIn(t, "alice@x.io")

	req := httptest.NewRequest(http.MethodPost, "/admin/system-config/reseed", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sessID})
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("admin POST status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/admin/system-config" {
		t.Errorf("Location = %q, want /admin/system-config", loc)
	}
}

// TestNav_ShowsSystemConfigForAdmin covers AC3: hamburger renders the
// System Config link only when IsGlobalAdmin is true.
func TestNav_ShowsSystemConfigForAdmin(t *testing.T) {
	f := newAdminFixture(t, "alice@x.io")
	sessID := f.signIn(t, "alice@x.io")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sessID})
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)

	if !strings.Contains(rec.Body.String(), `data-testid="nav-system-config-link"`) {
		t.Errorf("admin nav missing system-config link")
	}
}

// TestNav_HidesSystemConfigForNonAdmin: non-admin sessions do NOT see
// the System Config link in their hamburger.
func TestNav_HidesSystemConfigForNonAdmin(t *testing.T) {
	f := newAdminFixture(t, "alice@x.io")
	sessID := f.signIn(t, "bob@x.io")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sessID})
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)

	if strings.Contains(rec.Body.String(), `data-testid="nav-system-config-link"`) {
		t.Errorf("non-admin nav still shows system-config link")
	}
}
