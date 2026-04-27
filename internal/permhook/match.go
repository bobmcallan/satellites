// Package permhook resolves a tool call's allow/deny decision against
// the active CI's allocated agent (or the session-default agent when no
// CI is claimed). story_c08856b2.
package permhook

import "strings"

// Match returns true when at least one pattern in patterns admits tool.
//
// Pattern shapes supported:
//   - exact equality (e.g. "Bash:git_status" matches "Bash:git_status")
//   - prefix-glob ending in "*" (e.g. "Bash:git_*" matches "Bash:git_status")
//   - bare "*" matches everything
//   - "<scope>:**" recurses (e.g. "Edit:**" matches "Edit:internal/foo.go")
//
// Matching is intentionally simple — the v3 enforce hook used the same
// shape; richer regex/glob can land in a follow-up if needed.
func Match(patterns []string, tool string) bool {
	for _, p := range patterns {
		if matchOne(p, tool) {
			return true
		}
	}
	return false
}

func matchOne(pattern, tool string) bool {
	if pattern == "" {
		return false
	}
	if pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, ":**") {
		prefix := strings.TrimSuffix(pattern, "**")
		return strings.HasPrefix(tool, prefix)
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(tool, prefix)
	}
	return pattern == tool
}
