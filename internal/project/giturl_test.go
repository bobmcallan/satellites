package project

import "testing"

func TestCanonicaliseGitRemote(t *testing.T) {
	cases := []struct {
		in   string
		want string
		err  bool
	}{
		{"", "", false},
		{"  ", "", false},

		// SSH shorthand
		{"git@github.com:bobmcallan/satellites.git", "https://github.com/bobmcallan/satellites", false},
		{"git@github.com:bobmcallan/satellites", "https://github.com/bobmcallan/satellites", false},

		// ssh://
		{"ssh://git@github.com/bobmcallan/satellites.git", "https://github.com/bobmcallan/satellites", false},
		{"ssh://github.com/bobmcallan/satellites", "https://github.com/bobmcallan/satellites", false},

		// git://
		{"git://github.com/bobmcallan/satellites.git", "https://github.com/bobmcallan/satellites", false},

		// https / http / mixed case
		{"https://github.com/bobmcallan/satellites.git/", "https://github.com/bobmcallan/satellites", false},
		{"HTTPS://GitHub.com/bobmcallan/satellites", "https://github.com/bobmcallan/satellites", false},
		{"http://gitlab.example.com/group/repo.git", "https://gitlab.example.com/group/repo", false},

		// Scoped (<scope>:<host>/<owner>/<repo>) — agents see these
		// when an upstream tool prefixes the remote with a user or
		// org namespace.
		{"bobmcallan:github.com/bobmcallan/satellites", "https://github.com/bobmcallan/satellites", false},
		{"bobmcallan:github.com/bobmcallan/satellites.git", "https://github.com/bobmcallan/satellites", false},

		// Bare (<host>/<owner>/<repo>) — no scheme, no scope.
		{"github.com/bobmcallan/satellites", "https://github.com/bobmcallan/satellites", false},
		{"github.com/bobmcallan/satellites.git", "https://github.com/bobmcallan/satellites", false},

		// Errors
		{"not-a-url", "", true},
		{"git@:owner/repo", "", true},
		{"https://github.com/", "", true},
		{"https://github.com", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := CanonicaliseGitRemote(tc.in)
			if tc.err {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNewID_Format(t *testing.T) {
	id := NewID()
	if len(id) != 13 || id[:5] != "proj_" {
		t.Fatalf("malformed id: %q", id)
	}
}
