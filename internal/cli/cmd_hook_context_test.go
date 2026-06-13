package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// TestBoundAlwaysParts pins the byte-ceiling bounding: whole parts are added
// until the next would exceed the ceiling, then it stops and reports
// truncation — never a partial part, never silent (epic:always-context AC4).
func TestBoundAlwaysParts(t *testing.T) {
	a := strings.Repeat("a", 100)
	b := strings.Repeat("b", 100)
	c := strings.Repeat("c", 100)

	// Under ceiling: all parts, no truncation.
	got, trunc := boundAlwaysParts([]string{a, b}, 1000)
	if trunc || !strings.Contains(got, a) || !strings.Contains(got, b) {
		t.Errorf("under ceiling should keep all parts untruncated: trunc=%v", trunc)
	}

	// Ceiling fits only the first part: second dropped, truncated reported.
	got, trunc = boundAlwaysParts([]string{a, b, c}, 150)
	if !trunc {
		t.Errorf("want truncated=true when parts exceed the ceiling")
	}
	if !strings.Contains(got, a) || strings.Contains(got, b) {
		t.Errorf("should keep the first whole part and drop the rest, got %d bytes", len(got))
	}

	// Empty input: nothing, not truncated.
	if got, trunc := boundAlwaysParts(nil, 100); got != "" || trunc {
		t.Errorf("empty parts → empty/untruncated, got %q trunc=%v", got, trunc)
	}
}

// stubStoryDocs installs an offline fetchStoryDoc backed by the given map and
// restores the original after the test. Keeps the hook-context tests hermetic —
// the re-anchor path now resolves the parent epic via the server, and these
// tests must not depend on a live one.
func stubStoryDocs(t *testing.T, docs map[string]storyDoc) {
	t.Helper()
	orig := fetchStoryDoc
	t.Cleanup(func() { fetchStoryDoc = orig })
	fetchStoryDoc = func(_ context.Context, id, _, _ string) (storyDoc, bool) {
		d, ok := docs[id]
		return d, ok
	}
}

// TestRunHookContext_ReanchorsOnStoryFetch: a PreToolUse story document_get for a
// parentless story emits the lightweight always re-anchor pointer and no epic
// objective (AC1/AC2).
func TestRunHookContext_ReanchorsOnStoryFetch(t *testing.T) {
	repo := accessTestRepo(t)
	stubStoryDocs(t, map[string]storyDoc{
		"sty_abc123": {Name: "lonely-story", ParentID: ""},
	})
	payload := map[string]any{
		"session_id": "sessA",
		"cwd":        repo,
		"tool_input": map[string]any{"id": "sty_abc123"},
	}
	var out, errBuf bytes.Buffer
	if err := runHookContext(context.Background(), jsonReader(t, payload), &out, &errBuf); err != nil {
		t.Fatalf("runHookContext: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "additionalContext") || !strings.Contains(s, "document index") {
		t.Fatalf("expected always re-anchor pointer on story fetch, got: %s", s)
	}
	if strings.Contains(s, "Epic objective") {
		t.Errorf("parentless story must not inject an epic objective, got: %s", s)
	}
}

// TestRunHookContext_InjectsEpicObjective: a PreToolUse fetch of a child whose
// parent carries a Purpose paragraph injects that epic objective alongside the
// re-anchor (AC1), and only the Purpose paragraph rides along (AC3).
func TestRunHookContext_InjectsEpicObjective(t *testing.T) {
	repo := accessTestRepo(t)
	stubStoryDocs(t, map[string]storyDoc{
		"sty_aaaa1111": {Name: "epic-objective-propagation", ParentID: "sty_bbbb2222"},
		"sty_bbbb2222": {Name: "goal-context", Body: "Push the epic's why to the executor.\n\n## Workflow\n\n```yaml\nstates: []\n```"},
	})
	payload := map[string]any{
		"cwd":        repo,
		"tool_input": map[string]any{"id": "sty_aaaa1111"},
	}
	var out, errBuf bytes.Buffer
	if err := runHookContext(context.Background(), jsonReader(t, payload), &out, &errBuf); err != nil {
		t.Fatalf("runHookContext: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "document index") {
		t.Errorf("re-anchor pointer must still be present, got: %s", s)
	}
	if !strings.Contains(s, "Epic objective") || !strings.Contains(s, "goal-context") || !strings.Contains(s, "Push the epic's why to the executor.") {
		t.Errorf("expected epic objective (title + purpose) injected, got: %s", s)
	}
	if strings.Contains(s, "states: []") {
		t.Errorf("only the Purpose paragraph should ride along, not the rest of the parent body, got: %s", s)
	}
}

// TestFormatEpicObjective_TruncatesExplicitly: an oversize Purpose paragraph is
// cut with a visible marker, never a silent partial (AC3).
func TestFormatEpicObjective_TruncatesExplicitly(t *testing.T) {
	big := strings.Repeat("x", epicObjectiveCeiling+500)
	out := formatEpicObjective("goal-context", big)
	if !strings.Contains(out, "truncated") {
		t.Errorf("oversize purpose must be explicitly marked truncated, got %d bytes without marker", len(out))
	}
	if strings.Contains(out, big) {
		t.Errorf("the full oversize purpose must not be emitted verbatim")
	}
	// Nothing to show → empty, never a bare heading.
	if got := formatEpicObjective("", "   \n\n   "); got != "" {
		t.Errorf("no title and no purpose → empty, got: %q", got)
	}
}

// TestFirstParagraph_SkipsHeadingsAndFences pins the Purpose-paragraph rule.
func TestFirstParagraph_SkipsHeadingsAndFences(t *testing.T) {
	if got := firstParagraph("# Heading\n\nThe real purpose.\n\nmore"); got != "The real purpose." {
		t.Errorf("should skip a leading heading, got: %q", got)
	}
	if got := firstParagraph("```\ncode\n```\n\nThe purpose."); got != "The purpose." {
		t.Errorf("should skip a leading fence, got: %q", got)
	}
	if got := firstParagraph("\n\n  \n\n"); got != "" {
		t.Errorf("blank body → empty, got: %q", got)
	}
}

// TestRunHookContext_SilentOnNonStoryFetch: a PreToolUse document_get with no
// story id injects nothing (a plain document fetch must not re-anchor).
func TestRunHookContext_SilentOnNonStoryFetch(t *testing.T) {
	repo := accessTestRepo(t)
	payload := map[string]any{
		"cwd":        repo,
		"tool_input": map[string]any{"name": "some-doc", "scope": "project"},
	}
	var out, errBuf bytes.Buffer
	if err := runHookContext(context.Background(), jsonReader(t, payload), &out, &errBuf); err != nil {
		t.Fatalf("runHookContext: %v", err)
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Errorf("non-story fetch should be silent, got: %s", out.String())
	}
}

// TestRunHookContext_SessionStartFailsOpen: a SessionStart event (no tool_input)
// in an unconfigured cwd injects nothing and never errors (fail-open).
func TestRunHookContext_SessionStartFailsOpen(t *testing.T) {
	payload := map[string]any{
		"session_id": "sessA",
		"cwd":        t.TempDir(), // no .satellites → unconfigured
	}
	var out, errBuf bytes.Buffer
	if err := runHookContext(context.Background(), jsonReader(t, payload), &out, &errBuf); err != nil {
		t.Fatalf("runHookContext should fail open, got err: %v", err)
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Errorf("unconfigured SessionStart should inject nothing, got: %s", out.String())
	}
}
