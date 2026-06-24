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

// TestLooksDated pins the per-run heuristic (sty_23e31b56): a name carrying a
// YYYY-MM-DD stamp is "dated" (a per-run artifact); a plain name is not.
func TestLooksDated(t *testing.T) {
	dated := []string{
		"Discovery scan 2026-06-25",
		"2026-01-01 portfolio review",
		"meridian-2026-12-31-report",
	}
	for _, n := range dated {
		if !looksDated(n) {
			t.Errorf("want dated, got not-dated for %q", n)
		}
	}
	plain := []string{
		"Implementation summary",
		"codegraph",
		"v1.2.3 release notes", // version, not a date
		"2026-6-5 short fields", // not zero-padded → not YYYY-MM-DD
	}
	for _, n := range plain {
		if looksDated(n) {
			t.Errorf("want not-dated, got dated for %q", n)
		}
	}
}

// TestDatedOutputTaskSteer pins the steer surface (sty_23e31b56): it fires only
// for a dated, non-summary output; it names the config gap when no task workflow
// resolves, and steers toward a story-created task when one does.
func TestDatedOutputTaskSteer(t *testing.T) {
	t.Run("plain name is silent", func(t *testing.T) {
		if w := datedOutputTaskSteer("Implementation summary", "document", true); w != "" {
			t.Fatalf("plain name must not steer, got %q", w)
		}
	})
	t.Run("dated summary is silent", func(t *testing.T) {
		// kind:summary is the one-shot gate artifact, never steered.
		if w := datedOutputTaskSteer("2026-06-25 summary", "summary", true); w != "" {
			t.Fatalf("dated summary must not steer, got %q", w)
		}
	})
	t.Run("dated non-summary with task workflow steers to task", func(t *testing.T) {
		w := datedOutputTaskSteer("Discovery scan 2026-06-25", "document", true)
		if w == "" || !strings.Contains(w, "re-runnable TASK") || !strings.Contains(w, "CREATE a governed task") {
			t.Fatalf("want task steer, got %q", w)
		}
		if strings.Contains(w, "CONFIG GAP") {
			t.Fatalf("must not name a config gap when a task workflow resolves: %q", w)
		}
	})
	t.Run("dated non-summary without task workflow names the gap", func(t *testing.T) {
		w := datedOutputTaskSteer("Discovery scan 2026-06-25", "document", false)
		if w == "" || !strings.Contains(w, "CONFIG GAP") || !strings.Contains(w, "satellites-task-workflow") {
			t.Fatalf("want config-gap steer, got %q", w)
		}
	})
}
