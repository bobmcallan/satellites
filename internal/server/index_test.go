package server

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/auth"
)

func newTestConfig(devMode bool) Config {
	return Config{
		Store:    auth.New((*sql.DB)(nil)),
		Sessions: auth.NewSessions([]byte("test-secret-fixed-for-determinism")),
		DevMode:  devMode,
	}
}

// withSession returns a request carrying a valid session cookie for the
// given user id (no DB lookup required — indexHandler tolerates a
// nil-DB store and just leaves UserEmail empty).
func withSession(cfg Config, method, target, userID string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	rec := httptest.NewRecorder()
	cfg.Sessions.Issue(rec, userID)
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}
	return req
}

func TestIndex_UnauthedRedirectsToLogin(t *testing.T) {
	cfg := newTestConfig(false)
	rec := httptest.NewRecorder()
	indexHandler(cfg)(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status: got %d want 303", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/login" {
		t.Errorf("location: got %q want /login", got)
	}
}

func TestIndex_AuthedRendersPortal_NoDev(t *testing.T) {
	cfg := newTestConfig(false)
	rec := httptest.NewRecorder()
	indexHandler(cfg)(rec, withSession(cfg, http.MethodGet, "/", "usr_test"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"SATELLITES", "PROJECTS", `data-field="version"`, `data-section="endpoints"`,
		`data-field="footer-name"`, `data-field="footer-email"`,
		"/static/alpine.min.js",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	if strings.Contains(body, `data-field="footer-version"`) {
		t.Error("footer-version rendered (was removed per V4 footer pattern)")
	}
	if strings.Contains(body, "dev mode users") {
		t.Error("dev-users section rendered when DevMode=false")
	}
}

func TestIndex_AuthedRendersPortal_DevMode(t *testing.T) {
	cfg := newTestConfig(true)
	rec := httptest.NewRecorder()
	indexHandler(cfg)(rec, withSession(cfg, http.MethodGet, "/", "usr_test"))

	body := rec.Body.String()
	if !strings.Contains(body, "dev mode users") {
		t.Error("dev-users section missing when DevMode=true")
	}
	if !strings.Contains(body, auth.DevAdminKey) || !strings.Contains(body, auth.DevUserKey) {
		t.Error("dev keys not rendered")
	}
}

func TestIndex_NotFoundOnUnknownPath(t *testing.T) {
	cfg := newTestConfig(false)
	rec := httptest.NewRecorder()
	indexHandler(cfg)(rec, httptest.NewRequest(http.MethodGet, "/some/random/path", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d want 404", rec.Code)
	}
}

func TestStatic_ServesStyles(t *testing.T) {
	srv := httptest.NewServer(staticHandler())
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/static/styles.css")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("content-type: got %q", ct)
	}
}
