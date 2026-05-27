package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/changelog"
)

func TestGroupByService_OrdersByFirstAppearance(t *testing.T) {
	now := time.Now().UTC()
	entries := []changelog.Entry{
		{ID: "cl_1", Service: "satellites", VersionTo: "v0.1", CreatedAt: now.Add(-3 * time.Hour)},
		{ID: "cl_2", Service: "satellites-server", VersionTo: "v0.1", CreatedAt: now.Add(-2 * time.Hour)},
		{ID: "cl_3", Service: "satellites", VersionTo: "v0.2", CreatedAt: now.Add(-1 * time.Hour)},
	}
	groups := groupByService(entries)
	if len(groups) != 2 {
		t.Fatalf("expected 2 service groups, got %d", len(groups))
	}
	if groups[0].Service != "satellites" {
		t.Errorf("first group: got %q want satellites", groups[0].Service)
	}
	if groups[1].Service != "satellites-server" {
		t.Errorf("second group: got %q want satellites-server", groups[1].Service)
	}
	if len(groups[0].Entries) != 2 {
		t.Errorf("satellites group entry count: got %d want 2", len(groups[0].Entries))
	}
}

func TestEffectiveDateAfter_NilOrdering(t *testing.T) {
	now := time.Now().UTC()
	earlier := now.Add(-time.Hour)
	a := changelog.Entry{CreatedAt: earlier, EffectiveDate: &now}
	b := changelog.Entry{CreatedAt: now, EffectiveDate: nil}
	if !effectiveDateAfter(a, b) {
		t.Error("entry with effective_date should sort before NULL even when older created_at")
	}
	if effectiveDateAfter(b, a) {
		t.Error("NULL effective_date should sort after non-NULL")
	}
	c := changelog.Entry{CreatedAt: now, EffectiveDate: nil}
	d := changelog.Entry{CreatedAt: earlier, EffectiveDate: nil}
	if !effectiveDateAfter(c, d) {
		t.Error("two NULL effective_dates should fall back to created_at DESC")
	}
}

func TestRenderMarkdown_EscapesScripts(t *testing.T) {
	// goldmark without WithUnsafe replaces raw HTML with a "raw HTML
	// omitted" comment — the strongest sanitisation (entire tag and
	// its contents are dropped, not just escaped). Sibling paragraphs
	// still render.
	html := renderMarkdown("<script>alert(1)</script>\n\nplain text")
	got := string(html)
	if strings.Contains(got, "<script>") {
		t.Errorf("raw <script> survived: %q", got)
	}
	if strings.Contains(got, "alert(1)") {
		t.Errorf("script body survived: %q", got)
	}
	if !strings.Contains(got, "plain text") {
		t.Errorf("body text dropped: %q", got)
	}
}

func TestRenderMarkdown_HappyPath(t *testing.T) {
	html := renderMarkdown("# Heading\n\n- item one\n- item two")
	got := string(html)
	if !strings.Contains(got, "<h1") {
		t.Errorf("heading not rendered: %q", got)
	}
	if !strings.Contains(got, "<li>item one</li>") {
		t.Errorf("list item not rendered: %q", got)
	}
}

func TestChangelogHandler_NotFoundOnExtraPath(t *testing.T) {
	// Path-suffix guard mirrors indexHandler's check — the handler is
	// registered against the bare "/changelog" pattern and must 404
	// any deeper URL.
	cfg := newTestConfig(false)
	prev := changelogStore
	changelogStore = &changelog.Store{}
	defer func() { changelogStore = prev }()

	rec := httptest.NewRecorder()
	changelogHandler(cfg)(rec, httptest.NewRequest(http.MethodGet, "/changelog/extra", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d want 404", rec.Code)
	}
}

func TestChangelogHandler_StoreNotConfigured(t *testing.T) {
	cfg := newTestConfig(false)
	prev := changelogStore
	changelogStore = nil
	defer func() { changelogStore = prev }()

	rec := httptest.NewRecorder()
	changelogHandler(cfg)(rec, httptest.NewRequest(http.MethodGet, "/changelog", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "not configured") {
		t.Errorf("body missing 'not configured': %q", rec.Body.String())
	}
}
