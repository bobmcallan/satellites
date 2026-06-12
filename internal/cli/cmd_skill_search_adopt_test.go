package cli

import (
	"strings"
	"testing"
)

func TestInjectForkedFrom(t *testing.T) {
	t.Run("inside existing frontmatter", func(t *testing.T) {
		body := "---\nname: s\ndescription: d\n---\n\n# S\n"
		got := injectForkedFrom(body, "proj_a/s@3")
		want := "---\nname: s\ndescription: d\nforked-from: proj_a/s@3\n---\n\n# S\n"
		if got != want {
			t.Fatalf("placement wrong:\n got: %q\nwant: %q", got, want)
		}
	})

	t.Run("no frontmatter wraps a fresh block", func(t *testing.T) {
		got := injectForkedFrom("# S\n", "proj_a/s@1")
		if !strings.HasPrefix(got, "---\nforked-from: proj_a/s@1\n---\n") {
			t.Fatalf("missing wrapped block: %q", got)
		}
		if !strings.Contains(got, "# S") {
			t.Fatalf("body lost: %q", got)
		}
	})

	t.Run("re-adopt replaces the stamp", func(t *testing.T) {
		body := "---\nname: s\nforked-from: proj_a/s@1\n---\n# S\n"
		got := injectForkedFrom(body, "proj_b/s@7")
		if strings.Contains(got, "proj_a/s@1") {
			t.Fatalf("stale stamp survived: %q", got)
		}
		if n := strings.Count(got, "forked-from:"); n != 1 {
			t.Fatalf("stamp count=%d want 1: %q", n, got)
		}
	})
}

// TestReviewContent_FrontmatterExempt pins the AC4 enabler: substrate ids in
// the frontmatter (a forked-from stamp's publisher project id) pass the
// strict review, while the same id in authored prose still blocks.
func TestReviewContent_FrontmatterExempt(t *testing.T) {
	fmOnly := "---\nname: s\nforked-from: proj_60641e91/s@2\n---\n\n# S\n\nClean prose.\n"
	if findings := reviewContent(fmOnly); len(findings) != 0 {
		t.Fatalf("frontmatter id flagged: %+v", findings)
	}
	prose := "---\nname: s\n---\n\nTracked in sty_deadbeef.\n"
	if findings := reviewContent(prose); len(findings) != 1 {
		t.Fatalf("prose id not flagged exactly once: %+v", findings)
	}
	// A markdown horizontal rule mid-document does not open a fake
	// frontmatter zone that would swallow later prose ids.
	rule := "# S\n\n---\n\nTracked in sty_deadbeef.\n"
	if findings := reviewContent(rule); len(findings) != 1 {
		t.Fatalf("id after horizontal rule not flagged: %+v", findings)
	}
}
