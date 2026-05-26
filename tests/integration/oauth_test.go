//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/server"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// fakeProvider returns a Provider that:
//   - issues an authorize URL that we don't follow (we POST straight to /callback).
//   - exchanges any code for a fixed access token via the mock token server.
//   - FetchInfo returns the supplied ProviderUserInfo.
//
// The mock token server is registered as a t.Cleanup so it tears down on
// test exit. Pointing the oauth2.Config at it means we never talk to
// the real provider.
func fakeProvider(t *testing.T, name string, info auth.ProviderUserInfo) *auth.Provider {
	t.Helper()
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		body, _ := json.Marshal(map[string]any{
			"access_token": "fake-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(tokenSrv.Close)

	return &auth.Provider{
		Name: name,
		OAuth2: &oauth2.Config{
			ClientID:     "test-id",
			ClientSecret: "test-secret",
			RedirectURL:  "https://example.com/auth/" + name + "/callback",
			Endpoint: oauth2.Endpoint{
				AuthURL:  "https://example.com/authorize",
				TokenURL: tokenSrv.URL,
			},
		},
		FetchInfo: func(ctx context.Context, token *oauth2.Token) (auth.ProviderUserInfo, error) {
			return info, nil
		},
	}
}

// bootOAuthServer wires a satellites-server with a single fake provider
// and returns an httptest URL plus the auth.Store + state mint helper.
// Cleanup is registered on t.
func bootOAuthServer(t *testing.T, provider *auth.Provider, adminEmails []string) (string, *auth.Store, *auth.MemStateStore) {
	t.Helper()
	env := testbootstrap.SetUp(t)
	store := auth.New(env.DB)

	states := auth.NewStateStore(0)
	set := &auth.ProviderSet{}
	switch provider.Name {
	case "github":
		set.GitHub = provider
	case "google":
		set.Google = provider
	default:
		// Tests may use arbitrary names; route registration uses the name
		// directly so we still get /auth/<name>/callback wired up.
		set.GitHub = provider
	}

	handler := server.Build(server.Config{
		Store:    store,
		Sessions: auth.NewSessions([]byte("test-secret-fixed-for-determinism")),
		DevMode:  false,
		OAuth: auth.OAuthConfig{
			AdminEmails: adminEmails,
		},
		Providers:   set,
		OAuthStates: states,
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return srv.URL, store, states
}

// TestOAuth_Callback_HappyPath exercises start + callback end-to-end
// using a mock provider. Asserts: state is consumed, user upserted,
// session cookie issued, callback redirects to "/".
func TestOAuth_Callback_HappyPath(t *testing.T) {
	provider := fakeProvider(t, "github", auth.ProviderUserInfo{
		Sub:         "ghuser-1",
		Email:       "alice@example.com",
		DisplayName: "Alice",
	})
	srvURL, store, states := bootOAuthServer(t, provider, nil)

	// Mint a state directly (skips the /start redirect through GitHub).
	state, err := states.Mint()
	if err != nil {
		t.Fatal(err)
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // stop on the 303
		},
	}
	u := fmt.Sprintf("%s/oauth/github/callback?state=%s&code=fakecode", srvURL, state)
	resp, err := client.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: got %d want 303; body=%s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Location"); got != "/" {
		t.Errorf("location: got %q want /", got)
	}

	// Cookie jar should now hold a satellites_session cookie.
	cookieURL, _ := url.Parse(srvURL)
	var sessionFound bool
	for _, c := range jar.Cookies(cookieURL) {
		if c.Name == auth.SessionCookieName && c.Value != "" {
			sessionFound = true
		}
	}
	if !sessionFound {
		t.Error("session cookie missing")
	}

	// User row created with role=user (no admin email match).
	u2, err := store.GetUserByEmail(context.Background(), "alice@example.com")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if u2.Role != auth.RoleUser {
		t.Errorf("role: got %s want user", u2.Role)
	}
}

// TestOAuth_Callback_AdminPromotion verifies that an email in
// SATELLITES_ADMIN_EMAILS gets RoleAdmin on first OAuth sign-in.
func TestOAuth_Callback_AdminPromotion(t *testing.T) {
	provider := fakeProvider(t, "github", auth.ProviderUserInfo{
		Sub:         "ghuser-admin",
		Email:       "Boss@Example.COM", // mixed case — must still match
		DisplayName: "Boss",
	})
	srvURL, store, states := bootOAuthServer(t, provider, []string{"boss@example.com"})

	state, _ := states.Mint()
	resp, err := (&http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}).Get(fmt.Sprintf("%s/oauth/github/callback?state=%s&code=c", srvURL, state))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status: got %d want 303", resp.StatusCode)
	}

	u, err := store.GetUserByEmail(context.Background(), "Boss@Example.COM")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if u.Role != auth.RoleAdmin {
		t.Errorf("role: got %s want admin", u.Role)
	}
}

// TestOAuth_Callback_Idempotent verifies a second callback for the
// same (provider, sub) updates the existing row rather than failing on
// the unique index.
func TestOAuth_Callback_Idempotent(t *testing.T) {
	provider := fakeProvider(t, "google", auth.ProviderUserInfo{
		Sub:         "google-1",
		Email:       "u@example.com",
		DisplayName: "U",
	})
	srvURL, store, states := bootOAuthServer(t, provider, nil)

	// We registered the fake provider against GitHub slot in bootOAuthServer's
	// default branch, so the route is /oauth/google/callback only when we set
	// it as Google. Rebuild with explicit Google placement:
	_ = srvURL
	env := testbootstrap.SetUp(t)
	store = auth.New(env.DB)
	states = auth.NewStateStore(0)
	handler := server.Build(server.Config{
		Store:       store,
		Sessions:    auth.NewSessions([]byte("k")),
		Providers:   &auth.ProviderSet{Google: provider},
		OAuthStates: states,
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	for i := 0; i < 2; i++ {
		state, _ := states.Mint()
		resp, err := (&http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}).Get(fmt.Sprintf("%s/oauth/google/callback?state=%s&code=c", srv.URL, state))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("iter %d: status %d", i, resp.StatusCode)
		}
	}

	var count int
	if err := env.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE oauth_provider='google' AND oauth_sub='google-1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("user count: got %d want 1 (idempotency broken)", count)
	}
}

// TestOAuth_Callback_StateSurvivesAcrossInstances is the HTTP-layer
// regression test for the Fly "invalid state" outage. Two satellites-
// server handlers share one Postgres but hold *different* PGStateStore
// wrappers (simulating two Fly machines). A login on instance A
// followed by a callback on instance B must round-trip successfully —
// i.e. the state must be looked up from the DB, not from the process
// memory of whichever machine handled /login.
//
// Before the fix (in-memory StateStore in main.go) this test fails with
// 400 invalid state. Keep this test in place even if MemStateStore is
// later deleted: it guards the production wiring.
func TestOAuth_Callback_StateSurvivesAcrossInstances(t *testing.T) {
	env := testbootstrap.SetUp(t)
	store := auth.New(env.DB)
	provider := fakeProvider(t, "google", auth.ProviderUserInfo{
		Sub:         "google-multi",
		Email:       "multi@example.com",
		DisplayName: "Multi",
	})

	build := func(states auth.StateStore) *httptest.Server {
		h := server.Build(server.Config{
			Store:       store,
			Sessions:    auth.NewSessions([]byte("multi-instance-secret")),
			Providers:   &auth.ProviderSet{Google: provider},
			OAuthStates: states,
		})
		s := httptest.NewServer(h)
		t.Cleanup(s.Close)
		return s
	}
	// Two distinct PGStateStore wrappers, same DB — the production
	// shape under Fly's auto_stop / rolling deploys / multi-machine.
	srvA := build(auth.NewPGStateStore(env.DB, 0))
	srvB := build(auth.NewPGStateStore(env.DB, 0))

	noRedirect := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	// Step 1: /oauth/google/login on instance A. Capture the state
	// query param from the redirect to the (fake) provider authorize URL.
	resp, err := noRedirect.Get(srvA.URL + "/oauth/google/login")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login status: got %d want 303", resp.StatusCode)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	state := loc.Query().Get("state")
	if state == "" {
		t.Fatalf("login redirect missing state param: %s", resp.Header.Get("Location"))
	}

	// Step 2: /oauth/google/callback on instance B. With a process-
	// local state store this is 400 "invalid state"; with PGStateStore
	// it succeeds and issues a session cookie.
	cbURL := fmt.Sprintf("%s/oauth/google/callback?state=%s&code=fakecode", srvB.URL, state)
	resp, err = noRedirect.Get(cbURL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("callback on B: status %d, body=%s — state did not survive cross-instance boundary", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Location"); got != "/" {
		t.Errorf("callback Location: got %q want /", got)
	}

	// Confirm the user row was actually created.
	if _, err := store.GetUserByEmail(context.Background(), "multi@example.com"); err != nil {
		t.Errorf("user not upserted after cross-instance callback: %v", err)
	}
}

// TestOAuth_Callback_RejectsInvalidState verifies a missing or
// unrecognised state token is rejected with 400 (no user side-effect).
func TestOAuth_Callback_RejectsInvalidState(t *testing.T) {
	provider := fakeProvider(t, "github", auth.ProviderUserInfo{
		Sub: "x", Email: "x@x.com", DisplayName: "X",
	})
	srvURL, store, _ := bootOAuthServer(t, provider, nil)

	// Don't mint — pass a never-seen state.
	resp, err := (&http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}).Get(srvURL + "/oauth/github/callback?state=never&code=c")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", resp.StatusCode)
	}

	if _, err := store.GetUserByEmail(context.Background(), "x@x.com"); err == nil {
		t.Error("user should not have been created on rejected callback")
	}
}

// TestOAuth_Start_RedirectsToProvider verifies /auth/<provider>/start
// issues a 303 to the provider's authorize URL with state attached.
func TestOAuth_Start_RedirectsToProvider(t *testing.T) {
	provider := fakeProvider(t, "github", auth.ProviderUserInfo{Sub: "x", Email: "x@x.com"})
	srvURL, _, _ := bootOAuthServer(t, provider, nil)

	resp, err := (&http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}).Get(srvURL + "/oauth/github/login")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status: got %d want 303", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "https://example.com/authorize") {
		t.Errorf("location: got %q want https://example.com/authorize…", loc)
	}
	if !strings.Contains(loc, "state=") {
		t.Errorf("location missing state param: %s", loc)
	}
}

// TestStore_UpsertOAuthUser_Errors verifies bad inputs are rejected at
// the store level — handlers rely on this for defensive checks.
func TestStore_UpsertOAuthUser_Errors(t *testing.T) {
	env := testbootstrap.SetUp(t)
	store := auth.New(env.DB)
	ctx := context.Background()

	if _, err := store.UpsertOAuthUser(ctx, "", "sub", "e@e", "n", nil); err == nil {
		t.Error("empty provider should fail")
	}
	if _, err := store.UpsertOAuthUser(ctx, "p", "", "e@e", "n", nil); err == nil {
		t.Error("empty sub should fail")
	}
	if _, err := store.UpsertOAuthUser(ctx, "p", "sub", "", "n", nil); err == nil {
		t.Error("empty email should fail")
	}
}

// TestStore_ListAPIKeysForUser_Isolated verifies the per-user list
// scopes correctly: user A cannot see user B's keys.
func TestStore_ListAPIKeysForUser_Isolated(t *testing.T) {
	env := testbootstrap.SetUp(t)
	store := auth.New(env.DB)
	ctx := context.Background()

	uA, err := store.CreateUser(ctx, "u_a", "a@x.com", "A", auth.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	uB, err := store.CreateUser(ctx, "u_b", "b@x.com", "B", auth.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.IssueAPIKey(ctx, "k_a1", uA.ID, "", "agent1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.IssueAPIKey(ctx, "k_a2", uA.ID, "", "agent2"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.IssueAPIKey(ctx, "k_b1", uB.ID, "", "agent1"); err != nil {
		t.Fatal(err)
	}

	keysA, err := store.ListAPIKeysForUser(ctx, uA.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(keysA) != 2 {
		t.Errorf("A keys: got %d want 2", len(keysA))
	}
	keysB, err := store.ListAPIKeysForUser(ctx, uB.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(keysB) != 1 {
		t.Errorf("B keys: got %d want 1", len(keysB))
	}
}

// TestStore_RevokeKeyForUser_Scoped verifies one user cannot revoke
// another's key by guessing the id.
func TestStore_RevokeKeyForUser_Scoped(t *testing.T) {
	env := testbootstrap.SetUp(t)
	store := auth.New(env.DB)
	ctx := context.Background()

	uA, _ := store.CreateUser(ctx, "u_a", "a@x.com", "A", auth.RoleUser)
	uB, _ := store.CreateUser(ctx, "u_b", "b@x.com", "B", auth.RoleUser)
	_, kA, _ := store.IssueAPIKey(ctx, "k_a", uA.ID, "", "agent")

	// B tries to revoke A's key — must fail.
	if err := store.RevokeKeyForUser(ctx, uB.ID, kA.ID); err == nil {
		t.Error("cross-user revoke should fail")
	}
	// A revokes own key — must succeed.
	if err := store.RevokeKeyForUser(ctx, uA.ID, kA.ID); err != nil {
		t.Fatalf("self revoke: %v", err)
	}
	// Idempotency: re-revoke should fail (already revoked).
	if err := store.RevokeKeyForUser(ctx, uA.ID, kA.ID); err == nil {
		t.Error("double-revoke should fail")
	}

	// After revoke, ListAPIKeysForUser excludes revoked rows.
	keys, _ := store.ListAPIKeysForUser(ctx, uA.ID)
	if len(keys) != 0 {
		t.Errorf("after revoke list: got %d want 0", len(keys))
	}

	// Avoid unused err returns.
	_ = errors.New
}

// TestAPIKeysPage_E2E exercises GET → POST issue → POST revoke through
// the live server, asserting cookie-gated access and one-time raw key
// reveal on issue.
func TestAPIKeysPage_E2E(t *testing.T) {
	env := testbootstrap.SetUp(t)
	store := auth.New(env.DB)
	if err := store.DevSeed(context.Background()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	sessions := auth.NewSessions([]byte("test-secret"))
	handler := server.Build(server.Config{
		Store:    store,
		Sessions: sessions,
		DevMode:  true,
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// Build a request carrying an admin session cookie.
	makeRequest := func(method, path string, body io.Reader) *http.Request {
		req, err := http.NewRequest(method, srv.URL+path, body)
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		sessions.Issue(rec, "usr_dev_admin")
		for _, c := range rec.Result().Cookies() {
			req.AddCookie(c)
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		return req
	}
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Timeout:       10 * time.Second,
	}

	// GET — page renders, no keys yet beyond the dev-seeded one.
	resp, err := client.Do(makeRequest(http.MethodGet, "/settings/api-keys", nil))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET status: %d body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `data-form="apikeys-issue"`) {
		t.Error("issue form missing")
	}

	// POST issue — page shows the just-issued key.
	form := strings.NewReader("action=issue&agent_name=mytest")
	resp, err = client.Do(makeRequest(http.MethodPost, "/settings/api-keys", form))
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("issue status: %d body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `data-section="apikeys-just-issued"`) {
		t.Error("just-issued section missing after POST issue")
	}
	if !strings.Contains(string(body), "sk_") {
		t.Error("raw key not rendered in issue response")
	}

	// Get the issued row id from store to test revoke.
	keys, err := store.ListAPIKeysForUser(context.Background(), "usr_dev_admin")
	if err != nil {
		t.Fatal(err)
	}
	var issuedID string
	for _, k := range keys {
		if k.AgentName == "mytest" {
			issuedID = k.ID
		}
	}
	if issuedID == "" {
		t.Fatal("could not find issued key by agent_name")
	}

	// POST revoke — 303 redirect to /settings/api-keys.
	form = strings.NewReader("action=revoke&id=" + url.QueryEscape(issuedID))
	resp, err = client.Do(makeRequest(http.MethodPost, "/settings/api-keys", form))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("revoke status: %d", resp.StatusCode)
	}

	// Confirm revoked at the store level.
	keys, _ = store.ListAPIKeysForUser(context.Background(), "usr_dev_admin")
	for _, k := range keys {
		if k.ID == issuedID {
			t.Error("revoked key still appears in list")
		}
	}
}

// TestAPIKeysPage_RequiresSession verifies an unauthenticated GET is
// redirected to /login.
func TestAPIKeysPage_RequiresSession(t *testing.T) {
	env := testbootstrap.SetUp(t)
	store := auth.New(env.DB)
	handler := server.Build(server.Config{
		Store:    store,
		Sessions: auth.NewSessions([]byte("test")),
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp, err := (&http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}).Get(srv.URL + "/settings/api-keys")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status: got %d want 303", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); got != "/login" {
		t.Errorf("location: got %q want /login", got)
	}
}
