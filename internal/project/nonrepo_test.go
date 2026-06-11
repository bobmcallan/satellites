package project

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestDeriveNonRepo pins the single non_repo rule (sty_88a76023): a project is
// non-repo exactly when it has no bound git remote (empty/whitespace git_url).
func TestDeriveNonRepo(t *testing.T) {
	cases := map[string]bool{
		"":                       true,
		"   ":                    true,
		"\t\n":                   true,
		"https://github.com/o/r": false,
		"github.com/o/r":         false,
	}
	for in, want := range cases {
		if got := DeriveNonRepo(in); got != want {
			t.Errorf("DeriveNonRepo(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestProject_NonRepoSerialises confirms the derived field marshals under the
// non_repo JSON key, true for a repo-less project and false for a bound one.
func TestProject_NonRepoSerialises(t *testing.T) {
	for _, tc := range []struct {
		git  string
		want bool
	}{
		{"", true},
		{"https://github.com/o/r", false},
	} {
		p := Project{GitURLCanonical: tc.git, NonRepo: DeriveNonRepo(tc.git)}
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if !strings.Contains(string(raw), `"non_repo"`) {
			t.Errorf("non_repo key missing from JSON: %s", raw)
		}
		var back struct {
			NonRepo bool `json:"non_repo"`
		}
		if err := json.Unmarshal(raw, &back); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if back.NonRepo != tc.want {
			t.Errorf("git=%q: non_repo=%v, want %v", tc.git, back.NonRepo, tc.want)
		}
	}
}
