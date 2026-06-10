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

// TestRunHookContext_ReanchorsOnStoryFetch: a PreToolUse story document_get in a
// configured repo emits the lightweight always re-anchor pointer (AC2).
func TestRunHookContext_ReanchorsOnStoryFetch(t *testing.T) {
	repo := accessTestRepo(t)
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
