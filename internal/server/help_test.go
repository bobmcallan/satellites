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
	for _, want := range []string{"Executor", "Reviewer", "Operator", "story review", "status gate"} {
		if !strings.Contains(body, want) {
			t.Errorf("help page missing %q", want)
		}
	}
}

func TestHelpHandler_RendersWorkflowMonitor(t *testing.T) {
	// The monitor section documents the read-only observability surfaces the
	// qa-observability epic shipped. Assert each surface is named so the page
	// can't silently lose one.
	cfg := newTestConfig(false)

	rec := httptest.NewRecorder()
	helpHandler(cfg)(rec, httptest.NewRequest(http.MethodGet, "/help", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"workflow monitor",
		"Per-story trace",
		"satellites work status",
		"satellites evidence show",
		"satellites evidence audit",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("help page missing monitor content %q", want)
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
