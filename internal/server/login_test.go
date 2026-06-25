package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/bobmcallan/satellites/internal/auth"
)

func TestLogin_GET_RendersForm(t *testing.T) {
	cfg := newTestConfig(false)
	rec := httptest.NewRecorder()
	loginHandler(cfg)(rec, httptest.NewRequest(http.MethodGet, "/login", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"login", `name="email"`, `name="password"`, `data-form="login"`,
		`data-field="footer-name"`, `data-field="footer-email"`, `data-field="footer-version"`,
		"/static/alpine.min.js", `x-data`,
		// Install instructions for unauthenticated visitors (sty_48adcb56).
		`data-section="install"`, "install.sh | sh", "satellites init", "satellites auth",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	if strings.Contains(body, `data-section="dev-login"`) {
		t.Error("dev-login section shown when DevMode=false")
	}
}

func TestLogin_GET_DevModeShowsQuickButtons(t *testing.T) {
	cfg := newTestConfig(true)
	rec := httptest.NewRecorder()
	loginHandler(cfg)(rec, httptest.NewRequest(http.MethodGet, "/login", nil))

	body := rec.Body.String()
	if !strings.Contains(body, `data-section="dev-login"`) {
		t.Fatal("dev-login section missing")
	}
	if !strings.Contains(body, `data-action="dev-login-admin"`) {
		t.Error("dev-login-admin button missing")
	}
	if !strings.Contains(body, `data-action="dev-login-user"`) {
		t.Error("dev-login-user button missing")
	}
}

func TestLogin_POST_BadMethod(t *testing.T) {
	cfg := newTestConfig(false)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/login", nil)
	loginHandler(cfg)(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status: got %d want 405", rec.Code)
	}
}

func TestLogin_GET_RendersProviderButtons(t *testing.T) {
	cfg := newTestConfig(false)
	cfg.Providers = &auth.ProviderSet{
		GitHub: &auth.Provider{Name: "github", OAuth2: &oauth2.Config{}},
		Google: &auth.Provider{Name: "google", OAuth2: &oauth2.Config{}},
	}
	rec := httptest.NewRecorder()
	loginHandler(cfg)(rec, httptest.NewRequest(http.MethodGet, "/login", nil))

	body := rec.Body.String()
	for _, want := range []string{
		`data-section="oauth-providers"`,
		`data-action="oauth-github"`,
		`href="/oauth/github/login"`,
		`SIGN IN WITH GitHub`,
		`data-action="oauth-google"`,
		`href="/oauth/google/login"`,
		`SIGN IN WITH Google`,
		`OR USE EMAIL`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}

	// Primary login path is OAuth: provider buttons must appear BEFORE
	// the email/password form in document order.
	providersIdx := strings.Index(body, `data-section="oauth-providers"`)
	formIdx := strings.Index(body, `data-form="login"`)
	if providersIdx < 0 || formIdx < 0 {
		t.Fatalf("missing markers: providers=%d form=%d", providersIdx, formIdx)
	}
	if providersIdx > formIdx {
		t.Errorf("oauth-providers (%d) must appear before login form (%d) — OAuth is the primary path",
			providersIdx, formIdx)
	}
}

func TestLogin_GET_NoProviderButtonsWhenDisabled(t *testing.T) {
	cfg := newTestConfig(false)
	// cfg.Providers is nil
	rec := httptest.NewRecorder()
	loginHandler(cfg)(rec, httptest.NewRequest(http.MethodGet, "/login", nil))

	body := rec.Body.String()
	if strings.Contains(body, `data-section="oauth-providers"`) {
		t.Error("oauth-providers section rendered with no providers configured")
	}
}
