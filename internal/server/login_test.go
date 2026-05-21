package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
		`data-field="footer-name"`, `data-field="footer-version"`,
		"/static/alpine.min.js", `x-data`,
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
