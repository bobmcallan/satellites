package auth

import (
	"testing"
	"time"
)

func TestStateStore_MintConsume_RoundTrip(t *testing.T) {
	s := NewStateStore(0) // 0 -> default 10m
	id, err := s.Mint()
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("empty id")
	}
	if err := s.Consume(id); err != nil {
		t.Fatalf("first consume: %v", err)
	}
	if err := s.Consume(id); err == nil {
		t.Fatal("second consume should fail (replay)")
	}
}

func TestStateStore_Consume_EmptyOrUnknown(t *testing.T) {
	s := NewStateStore(time.Minute)
	if err := s.Consume(""); err == nil {
		t.Error("empty id should fail")
	}
	if err := s.Consume("never-minted"); err == nil {
		t.Error("unknown id should fail")
	}
}

func TestStateStore_Consume_Expired(t *testing.T) {
	s := NewStateStore(time.Millisecond)
	fixed := time.Now()
	s.now = func() time.Time { return fixed }
	id, err := s.Mint()
	if err != nil {
		t.Fatal(err)
	}
	// Advance virtual clock past the TTL.
	s.now = func() time.Time { return fixed.Add(time.Second) }
	if err := s.Consume(id); err == nil {
		t.Fatal("expired state should fail")
	}
}

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
