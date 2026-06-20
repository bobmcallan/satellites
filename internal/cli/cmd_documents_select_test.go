package cli

import "testing"

// TestSelectTargetByName: the per-artifact selector narrows an upload plan to one
// artifact — by resolved name or filename stem — and fails closed on a typo, so a
// single principle/skill/workflow review-and-writes decoupled from its siblings
// (sty_7b667ae7).
func TestSelectTargetByName(t *testing.T) {
	targets := []documentTarget{
		{Path: ".satellites/principles/yagni.md", Name: "yagni"},
		{Path: ".satellites/principles/broken-windows.md", Name: "broken-windows"},
		{Path: ".satellites/principles/story-execution-process.md", Name: "story-execution-process"},
	}

	// Selects exactly the named artifact — not its siblings.
	got, err := selectTargetByName(targets, "yagni")
	if err != nil {
		t.Fatalf("select yagni: %v", err)
	}
	if len(got) != 1 || got[0].Name != "yagni" {
		t.Fatalf("select yagni = %+v, want exactly [yagni]", got)
	}

	// Matches by filename stem too.
	if got, err := selectTargetByName(targets, "broken-windows"); err != nil || len(got) != 1 {
		t.Fatalf("select by stem: got %+v err %v", got, err)
	}

	// A typo'd selector fails closed (does not silently upload nothing or all).
	if _, err := selectTargetByName(targets, "yagnii"); err == nil {
		t.Fatalf("a non-matching selector must error, not select nothing silently")
	}
}
