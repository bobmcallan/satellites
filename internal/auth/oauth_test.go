package auth

import (
	"testing"
)

// StateStore round-trip / replay / expiry / empty-or-unknown coverage lives
// in the PGStateStore integration tests (oauth_state_store_postgres_test.go);
// the in-memory MemStateStore those unit tests exercised was removed as a
// rolling-deploy footgun (sty_38effee9).

func TestBuildProviderSet_DisabledWhenCredsMissing(t *testing.T) {
	set := BuildProviderSet(OAuthConfig{
		RedirectBaseURL: "https://example.com",
	})
	if set.GitHub != nil {
		t.Error("github should be nil without creds")
	}
	if set.Google != nil {
		t.Error("google should be nil without creds")
	}
	if len(set.Enabled()) != 0 {
		t.Errorf("enabled len: got %d want 0", len(set.Enabled()))
	}
}

func TestBuildProviderSet_DisabledWhenBaseMissing(t *testing.T) {
	set := BuildProviderSet(OAuthConfig{
		GitHubClientID:     "id",
		GitHubClientSecret: "sec",
	})
	if set.GitHub != nil {
		t.Error("github should be nil without redirect_base_url")
	}
}

func TestBuildProviderSet_BothEnabled(t *testing.T) {
	set := BuildProviderSet(OAuthConfig{
		GitHubClientID:     "gh-id",
		GitHubClientSecret: "gh-sec",
		GoogleClientID:     "g-id",
		GoogleClientSecret: "g-sec",
		RedirectBaseURL:    "https://example.com/",
	})
	if set.GitHub == nil || set.Google == nil {
		t.Fatal("expected both providers enabled")
	}
	if set.GitHub.OAuth2.RedirectURL != "https://example.com/oauth/github/callback" {
		t.Errorf("github redirect: %q", set.GitHub.OAuth2.RedirectURL)
	}
	if set.Google.OAuth2.RedirectURL != "https://example.com/oauth/google/callback" {
		t.Errorf("google redirect: %q", set.Google.OAuth2.RedirectURL)
	}
	names := []string{}
	for _, p := range set.Enabled() {
		names = append(names, p.Name)
	}
	if len(names) != 2 || names[0] != "google" || names[1] != "github" {
		t.Errorf("enabled order: %v", names)
	}
}

func TestParseAdminEmails(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"  ,  ", nil},
		{"a@x.com", []string{"a@x.com"}},
		{"  A@X.com , b@Y.com ,c@z.com", []string{"a@x.com", "b@y.com", "c@z.com"}},
	}
	for _, tt := range tests {
		got := ParseAdminEmails(tt.in)
		if len(got) != len(tt.want) {
			t.Errorf("%q: len got %d want %d (%v)", tt.in, len(got), len(tt.want), got)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("%q[%d]: got %q want %q", tt.in, i, got[i], tt.want[i])
			}
		}
	}
}
