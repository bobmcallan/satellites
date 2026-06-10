package cli

import (
	"strings"
	"testing"
)

// TestShouldFillHeadline pins the fill decision: only a document/principle
// (type "document") with an empty stored headline is generated. Skills are
// never processed and an existing headline is left alone — keeping re-upload
// idempotent (epic:always-context).
func TestShouldFillHeadline(t *testing.T) {
	cases := []struct {
		name    string
		docType string
		stored  string
		want    bool
	}{
		{"document, empty → fill", "document", "", true},
		{"document, whitespace → fill", "document", "   ", true},
		{"document, already set → skip", "document", "drive story to done", false},
		{"skill → never", "skill", "", false},
		{"story → never", "story", "", false},
		{"empty type → never", "", "", false},
	}
	for _, c := range cases {
		if got := shouldFillHeadline(c.docType, c.stored); got != c.want {
			t.Errorf("shouldFillHeadline(%q,%q) = %v, want %v", c.docType, c.stored, got, c.want)
		}
	}
}

// TestFirstHeadlineLine pins the completion → stored-line normalisation: first
// non-empty line, wrapping quotes/backticks stripped, clamped to the max.
func TestFirstHeadlineLine(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"plain", "fix small breakage now", "fix small breakage now"},
		{"leading blank lines", "\n\n  drive story to terminal state\n", "drive story to terminal state"},
		{"strips wrapping quotes", "\"reviewer gates enforce the contract\"", "reviewer gates enforce the contract"},
		{"strips backticks", "`gate verifies against the tree`", "gate verifies against the tree"},
		{"takes first of many", "headline one\nignored second line", "headline one"},
		{"empty", "", ""},
		{"whitespace only", "   \n\t\n", ""},
	}
	for _, c := range cases {
		if got := firstHeadlineLine(c.raw); got != c.want {
			t.Errorf("firstHeadlineLine(%q) = %q, want %q", c.raw, got, c.want)
		}
	}

	// Clamp: a body longer than the cap is truncated to the cap.
	long := strings.Repeat("x", headlineMaxLen+50)
	if got := firstHeadlineLine(long); len(got) > headlineMaxLen {
		t.Errorf("firstHeadlineLine did not clamp: len=%d > %d", len(got), headlineMaxLen)
	}
}
