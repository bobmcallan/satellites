package auth

import "testing"

// TestDisplayName pins the deterministic fallback chain (sty_fcf5477e):
// stored name → email local-part → raw user id.
func TestDisplayName(t *testing.T) {
	const id = "usr_oauth_google_108501074081366940596"
	cases := []struct {
		name string
		u    *User
		want string
	}{
		{"stored name wins", &User{DisplayName: "Bob McAllan", Email: "bob@x.local"}, "Bob McAllan"},
		{"email local-part when no name", &User{Email: "alice@example.com"}, "alice"},
		{"whitespace name is not a name", &User{DisplayName: "  ", Email: "carol@x.local"}, "carol"},
		{"raw id floor when nil user", nil, id},
		{"raw id floor when empty user", &User{}, id},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DisplayName(c.u, id); got != c.want {
				t.Fatalf("DisplayName = %q, want %q", got, c.want)
			}
		})
	}
}
