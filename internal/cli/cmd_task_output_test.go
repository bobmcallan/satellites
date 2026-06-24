package cli

import (
	"strings"
	"testing"
)

// TestBuildOutputTags pins B2 AC1: an output document carries its `type:` (kind),
// an optional `phase:`, and a `task:<id>` back-reference; the set is kvtag-
// normalized so type/phase appear at most once while the task reference survives.
func TestBuildOutputTags(t *testing.T) {
	t.Run("document with phase", func(t *testing.T) {
		got := buildOutputTags("document", "discovery", "", "tsk_abc")
		assertHasTag(t, got, "type:document")
		assertHasTag(t, got, "phase:discovery")
		assertHasTag(t, got, "task:tsk_abc")
		if len(got) != 3 {
			t.Fatalf("want 3 tags, got %v", got)
		}
	})

	t.Run("diagram kind maps to type:diagram", func(t *testing.T) {
		got := buildOutputTags("diagram", "discovery", "", "tsk_x")
		assertHasTag(t, got, "type:diagram")
		if outTagsContain(got, "type:document") {
			t.Fatalf("diagram kind must not also carry type:document: %v", got)
		}
	})

	t.Run("no phase omits phase tag", func(t *testing.T) {
		got := buildOutputTags("document", "", "", "tsk_y")
		for _, tg := range got {
			if strings.HasPrefix(tg, "phase:") {
				t.Fatalf("no phase given but phase tag present: %v", got)
			}
		}
		assertHasTag(t, got, "type:document")
		assertHasTag(t, got, "task:tsk_y")
	})

	t.Run("single type/phase after normalize", func(t *testing.T) {
		got := buildOutputTags("document", "build", "", "tsk_z")
		if countPrefix(got, "type:") != 1 || countPrefix(got, "phase:") != 1 {
			t.Fatalf("type/phase must each appear once: %v", got)
		}
		if countPrefix(got, "task:") != 1 {
			t.Fatalf("task reference must be preserved exactly once: %v", got)
		}
	})

	t.Run("format declares format: tag, omitted when empty", func(t *testing.T) {
		got := buildOutputTags("codegraph", "", "jgf-v1", "tsk_f")
		assertHasTag(t, got, "type:codegraph")
		assertHasTag(t, got, "format:jgf-v1")
		assertHasTag(t, got, "task:tsk_f")
		if countPrefix(got, "format:") != 1 {
			t.Fatalf("format must appear once: %v", got)
		}
		none := buildOutputTags("codegraph", "", "", "tsk_g")
		if countPrefix(none, "format:") != 0 {
			t.Fatalf("no format given but format tag present: %v", none)
		}
	})
}

func outTagsContain(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

func assertHasTag(t *testing.T, tags []string, want string) {
	t.Helper()
	if !outTagsContain(tags, want) {
		t.Fatalf("missing tag %q in %v", want, tags)
	}
}

func countPrefix(tags []string, prefix string) int {
	n := 0
	for _, t := range tags {
		if strings.HasPrefix(t, prefix) {
			n++
		}
	}
	return n
}
