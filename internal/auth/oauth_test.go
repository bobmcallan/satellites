package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	satarbor "github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/config"
)

func TestStateStoreRoundTrip(t *testing.T) {
	t.Parallel()
	s := NewStateStore(time.Minute)
	id, err := s.Mint()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Consume(id); err != nil {
		t.Errorf("Consume(valid) = %v", err)
	}
	// Second consume rejects (replay).
	if err := s.Consume(id); err == nil {
		t.Error("Consume replay should fail")
	}
}

func TestStateStoreExpiry(t *testing.T) {
	t.Parallel()
	s := NewStateStore(1 * time.Second)
	s.now = func() time.Time { return time.Unix(0, 0) }
	id, _ := s.Mint()
	s.now = func() time.Time { return time.Unix(3600, 0) }
	if err := s.Consume(id); err == nil {
		t.Error("Consume of expired state should fail")
	}
}

func TestStateStoreEmptyAndUnknown(t *testing.T) {
	t.Parallel()
	s := NewStateStore(time.Minute)
	if err := s.Consume(""); err == nil {
		t.Error("Consume(\"\") must fail")
	}
	if err := s.Consume("never-minted"); err == nil {
		t.Error("Consume(unknown) must fail")
	}
}

func TestBuildProviderSet_MissingCreds_Disabled(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{OAuthRedirectBaseURL: "https://x"} // no client ids
	set := BuildProviderSet(cfg)
	if set.Google != nil {
		t.Error("Google must be disabled without client id/secret")
	}
	if set.GitHub != nil {
		t.Error("GitHub must be disabled without client id/secret")
	}
}

func TestBuildProviderSet_BothEnabled(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		GoogleClientID: "gid", GoogleClientSecret: "gs",
		GithubClientID: "hid", GithubClientSecret: "hs",
		OAuthRedirectBaseURL: "https://x",
	}
	set := BuildProviderSet(cfg)
	if set.Google == nil || set.GitHub == nil {
		t.Fatalf("both providers must be enabled; got %+v", set)
	}
	if !strings.HasSuffix(set.Google.OAuth2.RedirectURL, "/auth/google/callback") {
		t.Errorf("Google redirect = %q", set.Google.OAuth2.RedirectURL)
	}
	if !strings.HasSuffix(set.GitHub.OAuth2.RedirectURL, "/auth/github/callback") {
		t.Errorf("GitHub redirect = %q", set.GitHub.OAuth2.RedirectURL)
	}
}

func TestOAuth_MissingProvider_Returns404(t *testing.T) {
	t.Parallel()
	users := NewMemoryUserStore()
	sessions := NewMemorySessionStore()
	h := &Handlers{
		Users:     users,
		Sessions:  sessions,
		Logger:    satarbor.New("info"),
		Cfg:       &config.Config{Env: "dev"},
		Providers: &ProviderSet{}, // no enabled providers
		States:    NewStateStore(time.Minute),
	}
	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest(http.MethodGet, "/auth/google/start", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("disabled provider start = %d, want 404", rec.Code)
	}
}

// TestOAuth_CallbackFull exercises a real oauth2 exchange against an httptest
// server pretending to be Google. Verifies state validation, token exchange,
// user-info fetch, upsert idempotence, session cookie set on the second
// request.
func TestOAuth_CallbackFull(t *testing.T) {
	t.Parallel()

	// Fake provider: auth-url is a no-op; token-url issues a fixed token;
	// userinfo-url returns a fixed email.
	var tokenHits int
	var userinfoHits int
	var fake *httptest.Server
	fake = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			tokenHits++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "tkn_abc",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
		case "/userinfo":
			userinfoHits++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"email": "alice@example.com",
				"name":  "Alice",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fake.Close)

	users := NewMemoryUserStore()
	sessions := NewMemorySessionStore()
	states := NewStateStore(time.Minute)
	providers := &ProviderSet{
		Google: &Provider{
			Name: "google",
			OAuth2: &oauth2.Config{
				ClientID:     "gid",
				ClientSecret: "gs",
				RedirectURL:  "http://localhost/auth/google/callback",
				Scopes:       []string{"openid", "email", "profile"},
				Endpoint: oauth2.Endpoint{
					AuthURL:  fake.URL + "/auth",
					TokenURL: fake.URL + "/token",
				},
			},
			FetchInfo: func(ctx context.Context, token *oauth2.Token) (ProviderUserInfo, error) {
				// Inline fetch against the fake userinfo.
				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fake.URL+"/userinfo", nil)
				req.Header.Set("Authorization", "Bearer "+token.AccessToken)
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					return ProviderUserInfo{}, err
				}
				defer resp.Body.Close()
				var body struct {
					Email string `json:"email"`
					Name  string `json:"name"`
				}
				if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
					return ProviderUserInfo{}, err
				}
				return ProviderUserInfo{Email: body.Email, DisplayName: body.Name}, nil
			},
		},
	}
	h := &Handlers{
		Users:     users,
		Sessions:  sessions,
		Logger:    satarbor.New("info"),
		Cfg:       &config.Config{Env: "dev"},
		Providers: providers,
		States:    states,
	}
	mux := http.NewServeMux()
	h.Register(mux)

	// Drive two callbacks — same email — to prove upsert idempotence.
	for attempt := 1; attempt <= 2; attempt++ {
		state, err := states.Mint()
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, "/auth/google/callback?state="+state+"&code=thecode", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("attempt %d callback status = %d, want 303; body=%s", attempt, rec.Code, rec.Body.String())
		}
		// Cookie set.
		var cookie *http.Cookie
		for _, c := range rec.Result().Cookies() {
			if c.Name == CookieName {
				cookie = c
			}
		}
		if cookie == nil {
			t.Fatalf("attempt %d no session cookie", attempt)
		}
	}

	// Idempotence: exactly one user row for google:alice@example.com.
	u, err := users.GetByEmail("google:alice@example.com")
	if err != nil {
		t.Fatalf("expected user row; got %v", err)
	}
	if u.Provider != "google" {
		t.Errorf("user.Provider = %q, want google", u.Provider)
	}
	if tokenHits != 2 || userinfoHits != 2 {
		t.Errorf("expected 2 token + 2 userinfo hits; got token=%d userinfo=%d", tokenHits, userinfoHits)
	}
}

func TestOAuth_Callback_RejectsBadState(t *testing.T) {
	t.Parallel()
	users := NewMemoryUserStore()
	sessions := NewMemorySessionStore()
	states := NewStateStore(time.Minute)
	providers := &ProviderSet{
		Google: &Provider{
			Name:   "google",
			OAuth2: &oauth2.Config{},
			FetchInfo: func(context.Context, *oauth2.Token) (ProviderUserInfo, error) {
				return ProviderUserInfo{Email: "x@y", DisplayName: "x"}, nil
			},
		},
	}
	h := &Handlers{
		Users:     users,
		Sessions:  sessions,
		Logger:    satarbor.New("info"),
		Cfg:       &config.Config{Env: "dev"},
		Providers: providers,
		States:    states,
	}
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/auth/google/callback?state=bogus&code=x", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad state status = %d, want 400", rec.Code)
	}
}
