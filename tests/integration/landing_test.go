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

// newBrowserCtx returns a fresh chromedp context backed by a headless
// Google Chrome subprocess. Timeouts + cleanup wired so tests fail fast
// rather than hang on a stuck browser.
func newBrowserCtx(t *testing.T) context.Context {
	t.Helper()
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

	return runCtx
}

// TestLogin_DevButtons_LandsOnPortal exercises the full login flow:
// unauthenticated → redirected to /login → dev-quick admin submit →
// portal visible with footer + nav + dev keys. This is V5's canonical
// browser-driven integration test.
func TestLogin_DevButtons_LandsOnPortal(t *testing.T) {
	env := testbootstrap.SetUpWithServer(t)
	ctx := newBrowserCtx(t)

	var (
		loginBrand string
		footerName string
		footerEmail string
		footerVer  string

		portalBrand string
		userEmail   string
		devAdminKey string
		devUserKey  string
		devMode     string
	)

	// Phase 1: unauthenticated navigation → login page.
	err := chromedp.Run(ctx,
		chromedp.Navigate(env.ServerURL+"/"),
		chromedp.WaitVisible(`form[data-form="login"]`, chromedp.ByQuery),
		chromedp.Text(`.brand`, &loginBrand, chromedp.ByQuery),
		chromedp.Text(`[data-field="footer-name"]`, &footerName, chromedp.ByQuery),
		chromedp.Text(`[data-field="footer-email"]`, &footerEmail, chromedp.ByQuery),
		chromedp.Text(`[data-field="footer-version"]`, &footerVer, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("phase1 (load login): %v", err)
	}
	if loginBrand != "SATELLITES" {
		t.Errorf("login .brand: got %q want SATELLITES", loginBrand)
	}
	if footerName == "" {
		t.Error("footer-name empty")
	}
	if !strings.Contains(footerEmail, "@") {
		t.Errorf("footer-email looks malformed: %q", footerEmail)
	}
	if footerVer == "" {
		t.Error("footer-version empty")
	}

	// Phase 2: click the dev-admin quick-login button.
	err = chromedp.Run(ctx,
		chromedp.Click(`button[data-action="dev-login-admin"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
		chromedp.Text(`.brand`, &portalBrand, chromedp.ByQuery),
		chromedp.Text(`[data-field="user-email"]`, &userEmail, chromedp.ByQuery),
		chromedp.Text(`[data-field="dev-mode"]`, &devMode, chromedp.ByQuery),
		chromedp.Text(`[data-field="dev-admin-key"]`, &devAdminKey, chromedp.ByQuery),
		chromedp.Text(`[data-field="dev-user-key"]`, &devUserKey, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("phase2 (dev login): %v", err)
	}
	if portalBrand != "SATELLITES" {
		t.Errorf("portal .brand: got %q want SATELLITES", portalBrand)
	}
	if userEmail != auth.DevAdminEmail {
		t.Errorf("user-email after admin login: got %q want %q", userEmail, auth.DevAdminEmail)
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

	// Phase 3: logout → back to login form.
	err = chromedp.Run(ctx,
		chromedp.Click(`button[data-action="logout"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`form[data-form="login"]`, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("phase3 (logout): %v", err)
	}
}

// TestLogin_BadCredentials_StaysOnLoginWithError submits invalid
// credentials and asserts the page re-renders with the error banner
// (no redirect to portal).
func TestLogin_BadCredentials_StaysOnLoginWithError(t *testing.T) {
	env := testbootstrap.SetUpWithServer(t)
	ctx := newBrowserCtx(t)

	var errMsg string
	err := chromedp.Run(ctx,
		chromedp.Navigate(env.ServerURL+"/login"),
		chromedp.WaitVisible(`form[data-form="login"]`, chromedp.ByQuery),
		chromedp.SendKeys(`input[name="email"]`, "nobody@nowhere.test", chromedp.ByQuery),
		chromedp.SendKeys(`input[name="password"]`, "wrong", chromedp.ByQuery),
		chromedp.Submit(`form[data-form="login"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-field="login-error"]`, chromedp.ByQuery),
		chromedp.Text(`[data-field="login-error"]`, &errMsg, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("chromedp: %v", err)
	}
	if !strings.Contains(strings.ToLower(errMsg), "invalid") {
		t.Errorf("error message: got %q want something containing 'invalid'", errMsg)
	}
}

// TestLandingPage_UnknownPath404s confirms unknown URLs still 404 after
// the login surface was added.
func TestLandingPage_UnknownPath404s(t *testing.T) {
	env := testbootstrap.SetUpWithServer(t)
	ctx := newBrowserCtx(t)

	var body string
	err := chromedp.Run(ctx,
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
