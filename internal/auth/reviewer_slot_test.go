package auth

import "testing"

// TestReviewerSlotAgentName covers sty_98a9bc0a: a story id scopes the
// reviewer slot so concurrent different-story gates get distinct slots; the
// `reviewer` prefix is always present so a reviewer slot never collides with
// an executor key's agent_name.
func TestReviewerSlotAgentName(t *testing.T) {
	tests := []struct {
		name, agent, story, want string
	}{
		{"per-story slot", "", "sty_abc123", "reviewer-sty_abc123"},
		{"story wins over agent", "custom", "sty_abc123", "reviewer-sty_abc123"},
		{"no story falls back to agent", "custom", "", "custom"},
		{"no story no agent -> bare reviewer", "", "", "reviewer"},
		{"whitespace story ignored", "", "   ", "reviewer"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := reviewerSlotAgentName(tc.agent, tc.story); got != tc.want {
				t.Errorf("reviewerSlotAgentName(%q,%q) = %q, want %q", tc.agent, tc.story, got, tc.want)
			}
		})
	}
	// Distinct stories must yield distinct slots — the core of the fix.
	if reviewerSlotAgentName("", "sty_a") == reviewerSlotAgentName("", "sty_b") {
		t.Error("distinct stories must map to distinct reviewer slots")
	}
}
