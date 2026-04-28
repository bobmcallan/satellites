package portal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/ledger"
)

// TestAdminKV_404ForNonAdmin — non-admin users see the page as 404.
// story_6dc33b90.
func TestAdminKV_404ForNonAdmin(t *testing.T) {
	f := newAdminFixture(t, "alice@x.io")
	sessID := f.signIn(t, "bob@x.io")

	req := httptest.NewRequest(http.MethodGet, "/admin/kv", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sessID})
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("non-admin GET status = %d, want 404", rec.Code)
	}
}

// TestAdminKV_RendersForAdmin_Empty — admin sees the empty-state panel.
func TestAdminKV_RendersForAdmin_Empty(t *testing.T) {
	f := newAdminFixture(t, "alice@x.io")
	sessID := f.signIn(t, "alice@x.io")

	req := httptest.NewRequest(http.MethodGet, "/admin/kv", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sessID})
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("admin GET status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`data-testid="admin-kv-panel"`,
		`data-testid="admin-kv-empty"`,
		`data-testid="admin-kv-set-form"`,
		`data-testid="admin-kv-resolve-form"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

// TestAdminKV_RendersRowsAndDelete — admin sees seeded rows + delete forms.
func TestAdminKV_RendersRowsAndDelete(t *testing.T) {
	f := newAdminFixture(t, "alice@x.io")
	sessID := f.signIn(t, "alice@x.io")

	// Seed a system-tier KV row directly via the ledger store.
	_, err := f.portal.ledger.Append(context.Background(), ledger.LedgerEntry{
		Type:    ledger.TypeKV,
		Tags:    []string{"scope:system", "key:default_theme"},
		Content: "dark",
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/kv", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sessID})
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "default_theme") {
		t.Errorf("body missing seeded key 'default_theme': %s", body)
	}
	if !strings.Contains(body, `data-testid="admin-kv-table"`) {
		t.Errorf("body missing kv table data-testid")
	}
	if !strings.Contains(body, `data-testid="admin-kv-delete-form"`) {
		t.Errorf("body missing delete form")
	}
}

// TestAdminKV_Set_AdminWritesAndRedirects — POST /admin/kv/set appends a
// row and redirects with a flash payload.
func TestAdminKV_Set_AdminWritesAndRedirects(t *testing.T) {
	f := newAdminFixture(t, "alice@x.io")
	sessID := f.signIn(t, "alice@x.io")

	form := url.Values{}
	form.Set("key", "tier")
	form.Set("value", "gold")
	req := httptest.NewRequest(http.MethodPost, "/admin/kv/set", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sessID})
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body = %s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/admin/kv?") {
		t.Errorf("redirect = %q, want prefix /admin/kv?", loc)
	}
	if !strings.Contains(loc, "flash") {
		t.Errorf("redirect missing flash query: %q", loc)
	}

	// Confirm the row landed via projection.
	rows, _ := ledger.KVProjectionScoped(context.Background(), f.portal.ledger,
		ledger.KVProjectionOptions{Scope: ledger.KVScopeSystem}, []string{""})
	if v := rows["tier"].Value; v != "gold" {
		t.Errorf("rows[tier] = %q, want gold", v)
	}
}

// TestAdminKV_Set_NonAdmin404 — non-admin POST to /admin/kv/set is 404.
func TestAdminKV_Set_NonAdmin404(t *testing.T) {
	f := newAdminFixture(t, "alice@x.io")
	sessID := f.signIn(t, "bob@x.io")

	form := url.Values{}
	form.Set("key", "tier")
	form.Set("value", "gold")
	req := httptest.NewRequest(http.MethodPost, "/admin/kv/set", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sessID})
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("non-admin POST status = %d, want 404", rec.Code)
	}
}

// TestAdminKV_Delete_AdminTombstones — POST /admin/kv/delete writes a
// tombstone and the projection drops the key.
func TestAdminKV_Delete_AdminTombstones(t *testing.T) {
	f := newAdminFixture(t, "alice@x.io")
	sessID := f.signIn(t, "alice@x.io")

	// Seed first.
	_, _ = f.portal.ledger.Append(context.Background(), ledger.LedgerEntry{
		Type: ledger.TypeKV, Tags: []string{"scope:system", "key:doomed"}, Content: "v1",
	}, time.Now().UTC())

	form := url.Values{}
	form.Set("key", "doomed")
	req := httptest.NewRequest(http.MethodPost, "/admin/kv/delete", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sessID})
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	rows, _ := ledger.KVProjectionScoped(context.Background(), f.portal.ledger,
		ledger.KVProjectionOptions{Scope: ledger.KVScopeSystem}, []string{""})
	if _, present := rows["doomed"]; present {
		t.Errorf("tombstoned key still present in projection: %+v", rows["doomed"])
	}
}

// TestAdminKV_ResolvedView — ?resolve=KEY shows the chain result.
func TestAdminKV_ResolvedView(t *testing.T) {
	f := newAdminFixture(t, "alice@x.io")
	sessID := f.signIn(t, "alice@x.io")

	// Seed a system value.
	_, _ = f.portal.ledger.Append(context.Background(), ledger.LedgerEntry{
		Type: ledger.TypeKV, Tags: []string{"scope:system", "key:lang"}, Content: "en",
	}, time.Now().UTC())

	req := httptest.NewRequest(http.MethodGet, "/admin/kv?resolve=lang", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sessID})
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-testid="admin-kv-resolved"`) {
		t.Errorf("body missing resolved-view payload: %s", body)
	}
	if !strings.Contains(body, "system") {
		t.Errorf("body missing 'system' tier in resolved view")
	}
	if !strings.Contains(body, "en") {
		t.Errorf("body missing resolved value 'en'")
	}
}

// TestAdminKV_ResolvedViewMiss — ?resolve=KEY for a non-existent key
// shows the empty-resolution message.
func TestAdminKV_ResolvedViewMiss(t *testing.T) {
	f := newAdminFixture(t, "alice@x.io")
	sessID := f.signIn(t, "alice@x.io")

	req := httptest.NewRequest(http.MethodGet, "/admin/kv?resolve=nonexistent", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sessID})
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `data-testid="admin-kv-resolved-miss"`) {
		t.Errorf("body missing resolved-miss payload")
	}
}

// silence unused import false positives
var _ = config.Version
