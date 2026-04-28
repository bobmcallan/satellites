package auth

import (
	"testing"
)

// TestLoadGlobalAdminEmails_ParsesCSV covers AC2 / AC3 of story_3548cde2:
// the env carrier accepts a comma-separated list, trims whitespace, and
// lowercases. Not parallel — uses t.Setenv.
func TestLoadGlobalAdminEmails_ParsesCSV(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want []string
	}{
		{name: "single", env: "alice@example.com", want: []string{"alice@example.com"}},
		{name: "multiple with whitespace", env: "alice@example.com ,  Bob@Example.com,carol@x.io", want: []string{"alice@example.com", "bob@example.com", "carol@x.io"}},
		{name: "empty", env: "", want: nil},
		{name: "trailing comma", env: "alice@example.com,", want: []string{"alice@example.com"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(GlobalAdminEmailsEnv, tc.env)
			got := LoadGlobalAdminEmails()
			if len(got) != len(tc.want) {
				t.Fatalf("len(got) = %d, want %d (got=%v)", len(got), len(tc.want), got)
			}
			for _, w := range tc.want {
				if _, ok := got[w]; !ok {
					t.Errorf("missing %q in %v", w, got)
				}
			}
		})
	}
}

// TestIsGlobalAdmin_FieldOrEnv covers AC2 / AC3: the helper returns true
// when either the persisted field is set OR the email is in the env set;
// false otherwise.
func TestIsGlobalAdmin_FieldOrEnv(t *testing.T) {
	t.Parallel()

	emails := map[string]struct{}{"bob@example.com": {}}

	cases := []struct {
		name string
		user User
		want bool
	}{
		{name: "field true", user: User{Email: "alice@x.io", GlobalAdmin: true}, want: true},
		{name: "env hit", user: User{Email: "BOB@example.com"}, want: true},
		{name: "env miss", user: User{Email: "carol@x.io"}, want: false},
		{name: "empty email and no field", user: User{}, want: false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IsGlobalAdmin(tc.user, emails); got != tc.want {
				t.Errorf("IsGlobalAdmin(%+v) = %v, want %v", tc.user, got, tc.want)
			}
		})
	}
}
