package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDocsMCP_UnauthedRedirectsToLogin(t *testing.T) {
	cfg := newTestConfig(true)
	rec := httptest.NewRecorder()
	docsMCPHandler(cfg)(rec, httptest.NewRequest(http.MethodGet, "/docs/mcp", nil))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status: got %d want 303", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/login" {
		t.Errorf("location: got %q want /login", got)
	}
}

func TestDocsMCP_AuthedRenders(t *testing.T) {
	cfg := newTestConfig(true)
	rec := httptest.NewRecorder()
	docsMCPHandler(cfg)(rec, withSession(cfg, http.MethodGet, "/docs/mcp", "usr_test"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"SATELLITES", `data-page="docs-mcp"`, `data-field="mcp-endpoint"`,
		"POST /mcp", `data-section="dev-keys"`, `data-section="example"`,
		`data-field="dev-admin-key"`, "sk_dev_admin", "/static/alpine.min.js",
		`data-field="footer-name"`, `data-field="footer-email"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	if strings.Contains(body, `data-field="footer-version"`) {
		t.Error("footer-version rendered (was removed per V4 footer pattern)")
	}
}

func TestDocsMCP_NonDev_HidesDevKeys(t *testing.T) {
	cfg := newTestConfig(false)
	rec := httptest.NewRecorder()
	docsMCPHandler(cfg)(rec, withSession(cfg, http.MethodGet, "/docs/mcp", "usr_test"))

	body := rec.Body.String()
	if strings.Contains(body, `data-section="dev-keys"`) {
		t.Error("dev-keys section rendered when DevMode=false")
	}
	if strings.Contains(body, "sk_dev_admin") {
		t.Error("dev admin key leaked when DevMode=false")
	}
}
