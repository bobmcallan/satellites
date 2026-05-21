//go:build integration

package integration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
	"github.com/chromedp/chromedp"
)

// TestLandingPage_RendersServerMetadata is the canonical browser-driven
// integration test: chromedp navigates to the live satellites-server
// landing page and verifies the DOM matches the server's runtime state.
//
// This is V5's default integration shape — tests assert behaviour
// through the same surface (a real browser hitting a real handler)
// operators see in production. Pure-Go assertions still exist for
// schema invariants and store-level behaviour, but anything user-facing
// goes through chromedp.
func TestLandingPage_RendersServerMetadata(t *testing.T) {
	env := testbootstrap.SetUpWithServer(t)

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(),
		append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.Flag("headless", true),
			chromedp.Flag("disable-gpu", true),
			chromedp.Flag("no-sandbox", true),
		)...,
	)
	t.Cleanup(cancelAlloc)

	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	t.Cleanup(cancelBrowser)

	runCtx, cancelRun := context.WithTimeout(browserCtx, 30*time.Second)
	t.Cleanup(cancelRun)

	var (
		brand        string
		version      string
		devMode      string
		devAdminKey  string
		devUserKey   string
		endpointRows []string
	)

	err := chromedp.Run(runCtx,
		chromedp.Navigate(env.ServerURL+"/"),
		chromedp.WaitVisible(`.brand`, chromedp.ByQuery),
		chromedp.Text(`.brand`, &brand, chromedp.ByQuery),
		chromedp.Text(`[data-field="version"]`, &version, chromedp.ByQuery),
		chromedp.Text(`[data-field="dev-mode"]`, &devMode, chromedp.ByQuery),
		chromedp.Text(`[data-field="dev-admin-key"]`, &devAdminKey, chromedp.ByQuery),
		chromedp.Text(`[data-field="dev-user-key"]`, &devUserKey, chromedp.ByQuery),
		chromedp.Evaluate(
			`Array.from(document.querySelectorAll('[data-field="endpoint"]')).map(r => r.innerText.replace(/\s+/g,' ').trim())`,
			&endpointRows,
		),
	)
	if err != nil {
		t.Fatalf("chromedp: %v", err)
	}

	if brand != "SATELLITES" {
		t.Errorf(".brand: got %q want SATELLITES", brand)
	}
	if version == "" {
		t.Error("version field empty")
	}
	if devMode != "true" {
		t.Errorf("dev-mode: got %q want true", devMode)
	}
	if devAdminKey != auth.DevAdminKey {
		t.Errorf("dev admin key: got %q want %q", devAdminKey, auth.DevAdminKey)
	}
	if devUserKey != auth.DevUserKey {
		t.Errorf("dev user key: got %q want %q", devUserKey, auth.DevUserKey)
	}

	if len(endpointRows) == 0 {
		t.Fatal("endpoint table empty")
	}
	wantContains := []string{"POST /mcp", "GET /oauth/github/login", "GET /oauth/github/callback"}
	joined := strings.Join(endpointRows, "\n")
	for _, w := range wantContains {
		if !strings.Contains(joined, w) {
			t.Errorf("endpoint row missing %q in:\n%s", w, joined)
		}
	}
}

// TestLandingPage_UnknownPath404s confirms unknown URLs still 404 —
// the root handler doesn't accidentally swallow every path.
func TestLandingPage_UnknownPath404s(t *testing.T) {
	env := testbootstrap.SetUpWithServer(t)

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(),
		append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.Flag("headless", true),
			chromedp.Flag("disable-gpu", true),
			chromedp.Flag("no-sandbox", true),
		)...,
	)
	t.Cleanup(cancelAlloc)

	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	t.Cleanup(cancelBrowser)

	runCtx, cancelRun := context.WithTimeout(browserCtx, 30*time.Second)
	t.Cleanup(cancelRun)

	var body string
	err := chromedp.Run(runCtx,
		chromedp.Navigate(env.ServerURL+"/no-such-path"),
		chromedp.Text(`body`, &body, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("chromedp: %v", err)
	}
	if !strings.Contains(body, "404") && !strings.Contains(strings.ToLower(body), "not found") {
		t.Errorf("expected 404 page text, got body: %q", body)
	}
}
