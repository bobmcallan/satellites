package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHelpHandler_RendersRoleModel(t *testing.T) {
	cfg := newTestConfig(false)

	rec := httptest.NewRecorder()
	helpHandler(cfg)(rec, httptest.NewRequest(http.MethodGet, "/help", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Executor", "Reviewer", "Operator", "story_request_review", "status gate"} {
		if !strings.Contains(body, want) {
			t.Errorf("help page missing %q", want)
		}
	}
}

func TestHelpHandler_NotFoundOnExtraPath(t *testing.T) {
	// Same bare-pattern guard as changelogHandler: registered against
	// "/help", must 404 any deeper URL.
	cfg := newTestConfig(false)

	rec := httptest.NewRecorder()
	helpHandler(cfg)(rec, httptest.NewRequest(http.MethodGet, "/help/extra", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d want 404", rec.Code)
	}
}
