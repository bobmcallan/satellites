package portal

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestThemeFromRequest_Default(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := themeFromRequest(r); got != "dark" {
		t.Errorf("themeFromRequest with no cookie = %q, want \"dark\"", got)
	}
}

func TestThemeFromRequest_Valid(t *testing.T) {
	cases := []string{"dark", "light", "system"}
	for _, mode := range cases {
		t.Run(mode, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.AddCookie(&http.Cookie{Name: themeCookieName, Value: mode})
			if got := themeFromRequest(r); got != mode {
				t.Errorf("themeFromRequest = %q, want %q", got, mode)
			}
		})
	}
}

func TestThemeFromRequest_InvalidValueFallsBackToDefault(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: themeCookieName, Value: "ultraviolet"})
	if got := themeFromRequest(r); got != "dark" {
		t.Errorf("themeFromRequest with invalid value = %q, want default \"dark\"", got)
	}
}

func TestResolveTheme(t *testing.T) {
	cases := []struct {
		mode string
		want string
	}{
		{"dark", "dark"},
		{"light", "light"},
		{"system", "dark"},
		{"", "dark"},
		{"unknown", "dark"},
	}
	for _, tc := range cases {
		if got := resolveTheme(tc.mode); got != tc.want {
			t.Errorf("resolveTheme(%q) = %q, want %q", tc.mode, got, tc.want)
		}
	}
}

func TestHandleThemeSet_ValidModes(t *testing.T) {
	p := &Portal{}
	for _, mode := range []string{"dark", "light", "system"} {
		t.Run(mode, func(t *testing.T) {
			form := url.Values{"mode": {mode}, "next": {"/projects"}}
			req := httptest.NewRequest(http.MethodPost, "/theme", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rr := httptest.NewRecorder()
			p.handleThemeSet(rr, req)

			if rr.Code != http.StatusSeeOther {
				t.Fatalf("status = %d, want 303", rr.Code)
			}
			if loc := rr.Header().Get("Location"); loc != "/projects" {
				t.Errorf("Location = %q, want /projects", loc)
			}
			cookies := rr.Result().Cookies()
			if len(cookies) != 1 {
				t.Fatalf("got %d cookies, want 1", len(cookies))
			}
			c := cookies[0]
			if c.Name != themeCookieName {
				t.Errorf("cookie name = %q, want %q", c.Name, themeCookieName)
			}
			if c.Value != mode {
				t.Errorf("cookie value = %q, want %q", c.Value, mode)
			}
			if c.Path != "/" {
				t.Errorf("cookie path = %q, want /", c.Path)
			}
			if c.SameSite != http.SameSiteLaxMode {
				t.Errorf("cookie SameSite = %v, want Lax", c.SameSite)
			}
			if c.MaxAge < 86400 {
				t.Errorf("cookie MaxAge = %d, want >= 1 day (got short-lived?)", c.MaxAge)
			}
		})
	}
}

func TestHandleThemeSet_InvalidMode(t *testing.T) {
	p := &Portal{}
	form := url.Values{"mode": {"ultraviolet"}, "next": {"/"}}
	req := httptest.NewRequest(http.MethodPost, "/theme", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	p.handleThemeSet(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 on invalid mode", rr.Code)
	}
	if len(rr.Result().Cookies()) != 0 {
		t.Errorf("cookie set on rejected request, want none")
	}
}

func TestHandleThemeSet_MethodNotAllowedOnGET(t *testing.T) {
	p := &Portal{}
	req := httptest.NewRequest(http.MethodGet, "/theme", nil)
	rr := httptest.NewRecorder()
	p.handleThemeSet(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405 on GET", rr.Code)
	}
}

func TestHandleThemeSet_RejectsExternalNext(t *testing.T) {
	p := &Portal{}
	form := url.Values{"mode": {"dark"}, "next": {"https://evil.example.com/x"}}
	req := httptest.NewRequest(http.MethodPost, "/theme", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	p.handleThemeSet(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want / (external next sanitized)", loc)
	}
}
