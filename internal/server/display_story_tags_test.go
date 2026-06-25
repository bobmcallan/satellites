package server

import (
	"strings"
	"testing"
)

// TestDisplayStoryTagsExcludesEstimateBasis pins sty_f10fcf69: the free-text
// estimate-basis tag is dropped from the story-row display set (chips +
// data-tags/data-search all derive from storyRow.Tags), while every other tag —
// including the numeric estimate/actual chips and the facet tags — survives.
func TestDisplayStoryTagsExcludesEstimateBasis(t *testing.T) {
	in := []string{
		"area:portal",
		"workflow:satellites-workflow",
		"sprint:2026-06-25",
		"order:12",
		"estimate-minutes:15",
		"estimate-tokens:22000",
		"actual-tokens:18000",
		"actual-minutes:12",
		"estimate-basis:single-site display filter; keep stored",
		"epic:sty_abcdef",
	}
	got := displayStoryTags(in)

	for _, g := range got {
		if strings.HasPrefix(g, "estimate-basis:") {
			t.Fatalf("estimate-basis tag must not appear in display tags, got %q", g)
		}
	}

	want := []string{
		"area:portal",
		"workflow:satellites-workflow",
		"sprint:2026-06-25",
		"order:12",
		"estimate-minutes:15",
		"estimate-tokens:22000",
		"actual-tokens:18000",
		"actual-minutes:12",
		"epic:sty_abcdef",
	}
	if len(got) != len(want) {
		t.Fatalf("display tags length = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("display tag[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestDisplayStoryTagsEmptyAndNoBasis pins that the filter is a no-op when no
// estimate-basis is present and tolerates an empty slice.
func TestDisplayStoryTagsEmptyAndNoBasis(t *testing.T) {
	if got := displayStoryTags(nil); len(got) != 0 {
		t.Fatalf("nil tags → %v, want empty", got)
	}
	in := []string{"area:portal", "order:1"}
	got := displayStoryTags(in)
	if len(got) != 2 || got[0] != "area:portal" || got[1] != "order:1" {
		t.Fatalf("no-basis passthrough = %v, want %v", got, in)
	}
}
