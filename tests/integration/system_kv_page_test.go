//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/server"
	"github.com/bobmcallan/satellites/internal/variable"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestSystemKVAdminPage exercises sty_5d03c1a0 — the /settings/system-kv
// page that lists + edits stored system KV rows via variable_set.
func TestSystemKVAdminPage(t *testing.T) {
	env := testbootstrap.SetUp(t)
	testbootstrap.Reset(t, env)

	authStore := auth.New(env.DB)
	if err := authStore.DevSeed(context.Background()); err != nil {
		t.Fatalf("dev seed: %v", err)
	}
	varStore := variable.New(env.DB)
	verb.SetAuthStore(authStore)
	verb.SetVariableStore(varStore)
	t.Cleanup(func() {
		verb.SetAuthStore(nil)
		verb.SetVariableStore(nil)
	})

	// Wire a stub computed resolver so the "computed" section has
	// something to render — exercises the page's split.
	verb.SetSystemVariableResolver(
		func(_ context.Context, name string) (string, bool) {
			if name == "version" {
				return "v0.test", true
			}
			return "", false
		},
		func(context.Context) []string { return []string{"version"} },
	)
	t.Cleanup(func() { verb.SetSystemVariableResolver(nil, nil) })

	ctx := context.Background()
	now := time.Date(2026, 5, 27, 18, 0, 0, 0, time.UTC)
	// Seed two stored knobs so the table has multiple rows.
	for _, p := range [][2]string{
		{"stories.page_size", "50"},
		{"feature.flag.x", "on"},
	} {
		if _, err := varStore.SeedSystem(ctx, p[0], p[1], now); err != nil {
			t.Fatalf("seed %s: %v", p[0], err)
		}
	}

	sessions := auth.NewSessions([]byte("system-kv-test-secret"))
	handler := server.Build(server.Config{
		Store:    authStore,
		Sessions: sessions,
		DevMode:  true,
	})

	authedReq := func(method, path string, body io.Reader) *http.Request {
		req := httptest.NewRequest(method, path, body)
		rec := httptest.NewRecorder()
		sessions.Issue(rec, "usr_dev_admin")
		for _, c := range rec.Result().Cookies() {
			req.AddCookie(c)
		}
		return req
	}
	get := func(t *testing.T, path string) (int, string) {
		t.Helper()
		req := authedReq(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		body, _ := io.ReadAll(rec.Result().Body)
		return rec.Code, string(body)
	}

	t.Run("page lists all scopes in one table with a scope column + filter", func(t *testing.T) {
		code, body := get(t, "/settings/system-kv")
		if code != http.StatusOK {
			t.Fatalf("status %d body=%s", code, body)
		}
		for _, want := range []string{
			`data-page="system-kv"`,
			`data-field="system-kv-table"`,
			`data-field="system-kv-search"`, // the name filter
			`data-field="kv-scope"`,         // the scope column cell
			`data-kv-scope="system"`,
			`data-kv-name="stories.page_size"`,
			`data-kv-name="feature.flag.x"`,
			`data-kv-name="version"`,   // computed row, now in the unified table
			`data-field="kv-readonly"`, // computed read-only marker
			`v0.test`,
			`data-form="kv-add"`,
			`data-field="kv-add-scope"`, // scope selector on the add form
		} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q", want)
			}
		}
		// The page no longer splits stored vs computed into separate sections.
		if strings.Contains(body, `data-section="system-kv-computed"`) {
			t.Error("page still renders a separate computed section; expected one unified table")
		}
		// All three rows (2 stored + 1 computed) live in the one table.
		if got := strings.Count(body, `data-field="kv-row"`); got != 3 {
			t.Errorf("expected 3 kv-row entries (2 stored + 1 computed), got %d", got)
		}
	})

	t.Run("inline edit updates the value", func(t *testing.T) {
		form := url.Values{}
		form.Set("action", "set")
		form.Set("name", "stories.page_size")
		form.Set("value", "200")
		req := authedReq(http.MethodPost, "/settings/system-kv", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusSeeOther && rec.Code != http.StatusOK {
			body, _ := io.ReadAll(rec.Result().Body)
			t.Fatalf("POST status %d: %s", rec.Code, string(body))
		}
		// Confirm round-trip via variable_get.
		raw, err := verb.Dispatch(ctx, "variable_get", json.RawMessage(
			`{"name":"stories.page_size","scope":"system"}`))
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		var got verb.VariableGetResponse
		_ = json.Unmarshal(raw, &got)
		if got.Value != "200" {
			t.Errorf("after edit: got value=%s want 200", got.Value)
		}
		// GET the page again — new value should appear in the input.
		_, body := get(t, "/settings/system-kv")
		if !strings.Contains(body, `value="200"`) {
			t.Errorf("page does not show new value 200 in any input")
		}
	})

	t.Run("computed name rejected with error flash", func(t *testing.T) {
		form := url.Values{}
		form.Set("action", "set")
		form.Set("name", "version")
		form.Set("value", "forged")
		req := authedReq(http.MethodPost, "/settings/system-kv", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		// Handler re-renders with FlashError on rejection.
		body, _ := io.ReadAll(rec.Result().Body)
		if !strings.Contains(string(body), `data-field="system-kv-error"`) {
			t.Errorf("expected system-kv-error rendered; body excerpt: %s", string(body)[:min(500, len(string(body)))])
		}
		// Computed value must not have been overwritten.
		raw, _ := verb.Dispatch(ctx, "variable_get", json.RawMessage(
			`{"name":"version","scope":"system"}`))
		var got verb.VariableGetResponse
		_ = json.Unmarshal(raw, &got)
		if got.Value == "forged" {
			t.Error("computed-system value overwritten via POST")
		}
	})

	t.Run("delete removes the row", func(t *testing.T) {
		form := url.Values{}
		form.Set("action", "delete")
		form.Set("name", "feature.flag.x")
		req := authedReq(http.MethodPost, "/settings/system-kv", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusSeeOther && rec.Code != http.StatusOK {
			t.Fatalf("delete status %d", rec.Code)
		}
		_, body := get(t, "/settings/system-kv")
		if strings.Contains(body, `data-kv-name="feature.flag.x"`) {
			t.Error("row survived delete")
		}
	})
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
