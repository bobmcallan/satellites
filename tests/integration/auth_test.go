package integration

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestLoginSetsCookieAndLogoutClears exercises the full auth loop against a
// real container: DevMode login sets a session cookie, logout clears it, and
// the empty body path is followed. Uses the existing testcontainer harness
// with DEV_USERNAME + DEV_PASSWORD env vars set.
func TestLoginSetsCookieAndLogoutClears(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping testcontainers test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	baseURL, stop := startServerContainerWithEnv(t, ctx, map[string]string{
		"DEV_USERNAME": "dev@local",
		"DEV_PASSWORD": "letmein",
	})
	defer stop()

	// HTTP client that refuses to follow redirects so we can inspect
	// Set-Cookie + Location on the 303 response.
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Login.
	form := url.Values{"username": {"dev@local"}, "password": {"letmein"}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/auth/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login status = %d, want 303", resp.StatusCode)
	}
	var session *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "satellites_session" {
			session = c
		}
	}
	if session == nil {
		t.Fatal("login response missing satellites_session cookie")
	}
	if session.Value == "" {
		t.Fatal("session cookie value is empty")
	}
	if !session.HttpOnly {
		t.Errorf("cookie HttpOnly = false, want true")
	}

	// Logout with the session cookie attached.
	logoutReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/auth/logout", nil)
	logoutReq.AddCookie(session)
	lresp, err := client.Do(logoutReq)
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	lresp.Body.Close()
	if lresp.StatusCode != http.StatusSeeOther {
		t.Fatalf("logout status = %d, want 303", lresp.StatusCode)
	}
	if loc := lresp.Header.Get("Location"); loc != "/login" {
		t.Errorf("logout Location = %q, want /login", loc)
	}
	var cleared bool
	for _, c := range lresp.Cookies() {
		if c.Name == "satellites_session" && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("logout did not emit a cookie-clearing Set-Cookie")
	}
}
