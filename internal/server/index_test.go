package server

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/auth"
)

// TestIndex_NotInDevMode renders the landing page without dev-mode
// blocks; asserts the SATELLITES title + version + endpoints section
// without relying on a live DB.
func TestIndex_NotInDevMode(t *testing.T) {
	cfg := Config{
		Store:   auth.New((*sql.DB)(nil)), // store unused on / path
		DevMode: false,
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	indexHandler(cfg)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type: got %q", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"SATELLITES", "PROJECTS", "MCP", `data-field="version"`, `data-section="endpoints"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	if strings.Contains(body, "dev mode users") {
		t.Error("dev-users section rendered when DevMode=false")
	}
}

func TestIndex_DevMode(t *testing.T) {
	cfg := Config{
		Store:   auth.New((*sql.DB)(nil)),
		DevMode: true,
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	indexHandler(cfg)(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "dev mode users") {
		t.Error("dev-users section missing when DevMode=true")
	}
	if !strings.Contains(body, auth.DevAdminKey) {
		t.Errorf("dev admin key not rendered (looking for %q)", auth.DevAdminKey)
	}
	if !strings.Contains(body, auth.DevUserKey) {
		t.Error("dev user key not rendered")
	}
}

func TestIndex_NotFoundOnUnknownPath(t *testing.T) {
	cfg := Config{Store: auth.New((*sql.DB)(nil)), DevMode: false}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/some/random/path", nil)
	indexHandler(cfg)(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d want 404 for unknown path", rec.Code)
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
