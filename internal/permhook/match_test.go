package permhook

import "testing"

func TestMatch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		patterns []string
		tool     string
		want     bool
	}{
		{name: "exact match", patterns: []string{"Bash:git_status"}, tool: "Bash:git_status", want: true},
		{name: "exact mismatch", patterns: []string{"Bash:git_status"}, tool: "Bash:git_push", want: false},
		{name: "prefix wildcard", patterns: []string{"Bash:git_*"}, tool: "Bash:git_status", want: true},
		{name: "prefix wildcard miss", patterns: []string{"Bash:git_*"}, tool: "Bash:go_test", want: false},
		{name: "double-star recurse", patterns: []string{"Edit:**"}, tool: "Edit:internal/foo.go", want: true},
		{name: "double-star miss other scope", patterns: []string{"Edit:**"}, tool: "Write:internal/foo.go", want: false},
		{name: "star matches all", patterns: []string{"*"}, tool: "anything", want: true},
		{name: "empty patterns", patterns: nil, tool: "Bash:git_status", want: false},
		{name: "any pattern matches", patterns: []string{"Read:**", "Bash:git_status"}, tool: "Bash:git_status", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Match(tc.patterns, tc.tool)
			if got != tc.want {
				t.Errorf("Match(%v, %q) = %v, want %v", tc.patterns, tc.tool, got, tc.want)
			}
		})
	}
}
