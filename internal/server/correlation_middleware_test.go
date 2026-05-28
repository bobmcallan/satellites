package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bobmcallan/satellites/internal/correlation"
)

func TestCorrelationMiddleware_StampsAllFiveIDs(t *testing.T) {
	var seen correlation.IDs
	var sawCtx bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, sawCtx = correlation.FromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	h := correlationMiddleware(inner)

	r := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	r.Header.Set(correlation.HeaderRunID, "run_abc")
	r.Header.Set(correlation.HeaderSessionID, "sess_xyz")
	r.Header.Set(correlation.HeaderStoryID, "sty_1")
	r.Header.Set(correlation.HeaderProjectID, "proj_2")
	r.Header.Set(correlation.HeaderWorkspaceID, "wksp_3")
	h.ServeHTTP(httptest.NewRecorder(), r)

	if !sawCtx {
		t.Fatal("expected context to carry IDs stamp")
	}
	want := correlation.IDs{
		RunID:       "run_abc",
		SessionID:   "sess_xyz",
		StoryID:     "sty_1",
		ProjectID:   "proj_2",
		WorkspaceID: "wksp_3",
	}
	if seen != want {
		t.Fatalf("mismatch: got=%+v want=%+v", seen, want)
	}
}

func TestCorrelationMiddleware_NoHeadersIsEmptyButStamped(t *testing.T) {
	var seen correlation.IDs
	var sawCtx bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, sawCtx = correlation.FromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	h := correlationMiddleware(inner)

	r := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	h.ServeHTTP(httptest.NewRecorder(), r)

	if !sawCtx {
		t.Fatal("expected stamp even with no headers (the IDs are just empty)")
	}
	if !seen.Empty() {
		t.Fatalf("expected empty IDs, got %+v", seen)
	}
}

func TestCorrelationMiddleware_PartialHeaders(t *testing.T) {
	var seen correlation.IDs
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = correlation.FromContext(r.Context())
	})
	h := correlationMiddleware(inner)

	r := httptest.NewRequest(http.MethodGet, "/mcp", nil).WithContext(context.Background())
	r.Header.Set(correlation.HeaderRunID, "run_only")
	h.ServeHTTP(httptest.NewRecorder(), r)

	if seen.RunID != "run_only" {
		t.Fatalf("RunID: got %q", seen.RunID)
	}
	if seen.SessionID != "" || seen.StoryID != "" || seen.ProjectID != "" || seen.WorkspaceID != "" {
		t.Fatalf("non-RunID fields should be empty: %+v", seen)
	}
}
