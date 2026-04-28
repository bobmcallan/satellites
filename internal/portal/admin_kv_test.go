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
	"github.com/bobmcallan/satellites/internal/ledger"
)

// TestAdminKV_RendersForAdmin_Empty — admin sees the empty-state panel
// for the default (system) scope.
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
		`data-testid="admin-kv-tabs"`,
		`data-testid="admin-kv-tab-system-active"`,
		`data-testid="admin-kv-tab-workspace"`,
		`data-testid="admin-kv-tab-project"`,
		`data-testid="admin-kv-tab-user"`,
		`data-testid="admin-kv-empty"`,
		`data-testid="admin-kv-set-form"`,
		`data-testid="admin-kv-resolve-form"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

// TestAdminKV_NonAdminGetsReadOnlyView — a non-admin authenticated
// caller sees the page (200) but no write affordances at the system
// scope tab.
func TestAdminKV_NonAdminGetsReadOnlyView(t *testing.T) {
	f := newAdminFixture(t, "alice@x.io")
	sessID := f.signIn(t, "bob@x.io")

	req := httptest.NewRequest(http.MethodGet, "/admin/kv", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sessID})
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("non-admin GET status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, `data-testid="admin-kv-set-form"`) {
		t.Errorf("non-admin should NOT see set-form at system scope")
	}
	if !strings.Contains(body, `data-testid="admin-kv-readonly"`) {
		t.Errorf("non-admin should see read-only banner")
	}
}

// TestAdminKV_NonAdmin_UserScopeIsWritable — at scope=user, any
// authenticated user may write to their own user-scope KV.
func TestAdminKV_NonAdmin_UserScopeIsWritable(t *testing.T) {
	f := newAdminFixture(t, "alice@x.io")
	sessID := f.signIn(t, "bob@x.io")

	req := httptest.NewRequest(http.MethodGet, "/admin/kv?scope=user", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sessID})
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-testid="admin-kv-set-form"`) {
		t.Errorf("non-admin should see set-form at scope=user (self only)")
	}
	if strings.Contains(body, `data-testid="admin-kv-readonly"`) {
		t.Errorf("non-admin at scope=user should NOT see read-only banner")
	}
}

// TestAdminKV_RendersRowsAndDelete — admin sees seeded rows + delete forms.
func TestAdminKV_RendersRowsAndDelete(t *testing.T) {
	f := newAdminFixture(t, "alice@x.io")
	sessID := f.signIn(t, "alice@x.io")

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
		t.Errorf("body missing seeded key: %s", body)
	}
	if !strings.Contains(body, `data-testid="admin-kv-delete-form"`) {
		t.Errorf("body missing delete form")
	}
}

// TestAdminKV_Set_AdminWritesAndRedirects — admin POST to /admin/kv/set
// writes a system-tier row.
func TestAdminKV_Set_AdminWritesAndRedirects(t *testing.T) {
	f := newAdminFixture(t, "alice@x.io")
	sessID := f.signIn(t, "alice@x.io")

	form := url.Values{}
	form.Set("scope", "system")
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
		t.Errorf("redirect = %q", loc)
	}
	rows, _ := ledger.KVProjectionScoped(context.Background(), f.portal.ledger,
		ledger.KVProjectionOptions{Scope: ledger.KVScopeSystem}, []string{""})
	if v := rows["tier"].Value; v != "gold" {
		t.Errorf("rows[tier] = %q, want gold", v)
	}
}

// TestAdminKV_Set_NonAdmin_SystemScopeForbidden — non-admin POST to
// scope=system gets a redirect with a forbidden flash.
func TestAdminKV_Set_NonAdmin_SystemScopeForbidden(t *testing.T) {
	f := newAdminFixture(t, "alice@x.io")
	sessID := f.signIn(t, "bob@x.io")

	form := url.Values{}
	form.Set("scope", "system")
	form.Set("key", "tier")
	form.Set("value", "gold")
	req := httptest.NewRequest(http.MethodPost, "/admin/kv/set", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sessID})
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 (with forbidden flash)", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "flash") || !strings.Contains(loc, "forbidden") {
		t.Errorf("redirect missing forbidden flash: %q", loc)
	}
	// And no row should have landed.
	rows, _ := ledger.KVProjectionScoped(context.Background(), f.portal.ledger,
		ledger.KVProjectionOptions{Scope: ledger.KVScopeSystem}, []string{""})
	if _, present := rows["tier"]; present {
		t.Errorf("system row should NOT have been written by non-admin")
	}
}

// TestAdminKV_Delete_AdminTombstones — admin delete tombstones the key.
func TestAdminKV_Delete_AdminTombstones(t *testing.T) {
	f := newAdminFixture(t, "alice@x.io")
	sessID := f.signIn(t, "alice@x.io")

	_, _ = f.portal.ledger.Append(context.Background(), ledger.LedgerEntry{
		Type: ledger.TypeKV, Tags: []string{"scope:system", "key:doomed"}, Content: "v1",
	}, time.Now().UTC())

	form := url.Values{}
	form.Set("scope", "system")
	form.Set("key", "doomed")
	req := httptest.NewRequest(http.MethodPost, "/admin/kv/delete", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sessID})
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", rec.Code)
	}
	rows, _ := ledger.KVProjectionScoped(context.Background(), f.portal.ledger,
		ledger.KVProjectionOptions{Scope: ledger.KVScopeSystem}, []string{""})
	if _, present := rows["doomed"]; present {
		t.Errorf("tombstoned key still present: %+v", rows["doomed"])
	}
}

// TestAdminKV_ResolvedView — ?resolve=KEY shows the chain result.
func TestAdminKV_ResolvedView(t *testing.T) {
	f := newAdminFixture(t, "alice@x.io")
	sessID := f.signIn(t, "alice@x.io")

	_, _ = f.portal.ledger.Append(context.Background(), ledger.LedgerEntry{
		Type: ledger.TypeKV, Tags: []string{"scope:system", "key:lang"}, Content: "en",
	}, time.Now().UTC())

	req := httptest.NewRequest(http.MethodGet, "/admin/kv?resolve=lang", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sessID})
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-testid="admin-kv-resolved"`) {
		t.Errorf("body missing resolved-view payload")
	}
}

// TestAdminKV_ResolvedViewMiss — empty-resolution renders correctly.
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
