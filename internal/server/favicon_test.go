package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestFaviconHandler covers sty_21d2288e: /favicon.ico is served from the
// embedded asset with an icon content-type and non-empty body, so every portal
// page gets a favicon from one root handler (no per-template <head> edits).
func TestFaviconHandler(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/favicon.ico", nil)
	faviconHandler()(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/x-icon" {
		t.Errorf("content-type = %q, want image/x-icon", ct)
	}
	if rec.Body.Len() == 0 {
		t.Error("favicon body is empty")
	}
	// Sanity: the embedded asset is a real ICO (magic: 0x00 0x00 0x01 0x00).
	b := rec.Body.Bytes()
	if len(b) < 4 || b[0] != 0x00 || b[1] != 0x00 || b[2] != 0x01 || b[3] != 0x00 {
		t.Errorf("body is not a valid ICO header: % x", b[:min(4, len(b))])
	}
}
