package cli

import (
	"context"
	"strings"
	"testing"
)

// TestBuildStoryOutputTags pins sty_1abc1bff AC1: a story output document carries
// its `type:` (kind), an optional `phase:`, an optional `format:`, and a
// `story:<id>` back-reference; the set is kvtag-normalized so the single-valued
// keys appear at most once while the story reference survives. Parity with the
// task-output tag builder.
func TestBuildStoryOutputTags(t *testing.T) {
	t.Run("summary with story reference", func(t *testing.T) {
		got := buildStoryOutputTags("summary", "", "", "sty_abc")
		assertHasTag(t, got, "type:summary")
		assertHasTag(t, got, "story:sty_abc")
		if len(got) != 2 {
			t.Fatalf("want 2 tags, got %v", got)
		}
	})

	t.Run("phase and format declared", func(t *testing.T) {
		got := buildStoryOutputTags("codegraph", "discovery", "jgf-v1", "sty_x")
		assertHasTag(t, got, "type:codegraph")
		assertHasTag(t, got, "phase:discovery")
		assertHasTag(t, got, "format:jgf-v1")
		assertHasTag(t, got, "story:sty_x")
	})

	t.Run("no phase/format omits those tags", func(t *testing.T) {
		got := buildStoryOutputTags("summary", "", "", "sty_y")
		if countPrefix(got, "phase:") != 0 || countPrefix(got, "format:") != 0 {
			t.Fatalf("no phase/format given but a tag is present: %v", got)
		}
	})

	t.Run("single-valued keys collapse, story reference preserved", func(t *testing.T) {
		got := buildStoryOutputTags("summary", "build", "v1", "sty_z")
		if countPrefix(got, "type:") != 1 || countPrefix(got, "phase:") != 1 || countPrefix(got, "format:") != 1 {
			t.Fatalf("type/phase/format must each appear once: %v", got)
		}
		if countPrefix(got, "story:") != 1 {
			t.Fatalf("story reference must be preserved exactly once: %v", got)
		}
	})
}

// TestRunStoryOutput_Refusals pins the pre-dispatch validation (AC4): an empty
// --name and an empty body are refused before any verb call, so a malformed
// invocation never creates a half-attached document.
func TestRunStoryOutput_Refusals(t *testing.T) {
	var sb strings.Builder
	if err := runStoryOutput(context.Background(), &sb, "", "", "sty_x", storyOutputOpts{Name: ""}); err == nil || !strings.Contains(err.Error(), "--name is required") {
		t.Fatalf("empty name must be refused, got %v", err)
	}
	if err := runStoryOutput(context.Background(), &sb, "", "", "sty_x", storyOutputOpts{Name: "Summary", Body: "   "}); err == nil || !strings.Contains(err.Error(), "empty body") {
		t.Fatalf("empty body must be refused, got %v", err)
	}
}
