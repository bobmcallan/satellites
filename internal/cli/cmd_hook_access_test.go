package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/cliconfig"
	"github.com/bobmcallan/satellites/internal/workstate"
)

// accessTestRepo builds a minimal configured repo (so findSatellitesRepoRoot
// resolves) and returns its root.
func accessTestRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	sat := filepath.Join(repo, ".satellites")
	if err := os.MkdirAll(sat, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sat, "satellites.toml"), []byte("project_id=\"proj_x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

// storePath is the engagement store the access hook actually reads — the
// resolved state.db (now <repo>/.satellites/state.db, beside index.db), so the
// test seeds where stateDBForRoot looks.
func storePath(repo string) string {
	return cliconfig.Config{}.ResolveStateDB(repo)
}

func jsonReader(t *testing.T, v any) *bytes.Reader {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(b)
}

// TestHookAccess_RemindsWhenNotEngaged: a story fetch with no live engagement
// injects an advisory reminder and records a candidate engagement (AC1, AC3).
func TestHookAccess_RemindsWhenNotEngaged(t *testing.T) {
	repo := accessTestRepo(t)
	payload := map[string]any{
		"session_id": "sessA",
		"cwd":        repo,
		"tool_name":  "mcp__satellites__document_get",
		"tool_input": map[string]any{"id": "sty_abc123"},
	}
	var out bytes.Buffer
	if err := runHookAccess(jsonReader(t, payload), &out, time.Now().UTC()); err != nil {
		t.Fatalf("runHookAccess: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "additionalContext") || !strings.Contains(s, "sty_abc123") || !strings.Contains(s, "work init") {
		t.Fatalf("expected advisory engage reminder, got: %s", s)
	}
	// It must NOT carry a permission decision (advisory — never blocks).
	if strings.Contains(s, "permissionDecision") {
		t.Errorf("access reminder must not emit a permission decision: %s", s)
	}
	// Candidate engagement recorded.
	store, err := workstate.Open(storePath(repo))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	eng, ok, _ := store.Current("sessA", "sty_abc123")
	if !ok {
		t.Fatalf("candidate engagement not recorded")
	}
	if isLiveEngagement(eng) {
		t.Errorf("candidate should not count as a live engagement, phase=%q", eng.Phase)
	}
}

// TestHookAccess_SilentWhenEngaged: a story already engaged in this session
// draws no reminder (AC1 silent-allow path).
func TestHookAccess_SilentWhenEngaged(t *testing.T) {
	repo := accessTestRepo(t)
	store, err := workstate.Open(storePath(repo))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(workstate.Event{Session: "sessA", Story: "sty_abc123", Phase: "in_progress", Kind: "engage", TS: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	store.Close()

	payload := map[string]any{
		"session_id": "sessA", "cwd": repo,
		"tool_name": "mcp__satellites__document_get", "tool_input": map[string]any{"id": "sty_abc123"},
	}
	var out bytes.Buffer
	if err := runHookAccess(jsonReader(t, payload), &out, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Errorf("expected silence for an engaged story, got: %s", out.String())
	}
}

// TestHookAccess_IgnoresNonStoryFetch: a non-story document_get (name/scope, no
// story id) is untouched — normal work is unaffected (AC4).
func TestHookAccess_IgnoresNonStoryFetch(t *testing.T) {
	repo := accessTestRepo(t)
	payload := map[string]any{
		"session_id": "sessA", "cwd": repo,
		"tool_name": "mcp__satellites__document_get", "tool_input": map[string]any{"name": "some-doc", "scope": "system"},
	}
	var out bytes.Buffer
	if err := runHookAccess(jsonReader(t, payload), &out, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Errorf("expected silence for a non-story fetch, got: %s", out.String())
	}
}

// TestHookPrompt_RemindsOnStoryID: a story id in the prompt injects the reminder (AC2).
func TestHookPrompt_RemindsOnStoryID(t *testing.T) {
	repo := accessTestRepo(t)
	payload := map[string]any{"session_id": "sessA", "cwd": repo, "prompt": "please implement sty_deadbeef now"}
	var out bytes.Buffer
	if err := runHookPrompt(jsonReader(t, payload), &out, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "sty_deadbeef") || !strings.Contains(s, "UserPromptSubmit") {
		t.Errorf("expected prompt engage reminder, got: %s", s)
	}
}

// TestHookPrompt_SilentWithoutStoryID: an ordinary prompt is untouched.
func TestHookPrompt_SilentWithoutStoryID(t *testing.T) {
	repo := accessTestRepo(t)
	payload := map[string]any{"session_id": "sessA", "cwd": repo, "prompt": "just chatting, no story here"}
	var out bytes.Buffer
	if err := runHookPrompt(jsonReader(t, payload), &out, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Errorf("expected silence, got: %s", out.String())
	}
}

// TestHookAccess_SubagentKeysToParentSession: a subagent's fetch carries
// parent_session_id; the engagement is keyed to the PARENT, so when the parent
// has already engaged the story the subagent draws no spurious reminder (AC3).
func TestHookAccess_SubagentKeysToParentSession(t *testing.T) {
	repo := accessTestRepo(t)
	store, err := workstate.Open(storePath(repo))
	if err != nil {
		t.Fatal(err)
	}
	// Parent engaged the story.
	if _, err := store.Append(workstate.Event{Session: "parentSess", Story: "sty_abc123", Phase: "in_progress", Kind: "engage", TS: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	store.Close()

	// Subagent (own session childSess) fetches the same story.
	payload := map[string]any{
		"session_id":        "childSess",
		"parent_session_id": "parentSess",
		"cwd":               repo,
		"tool_name":         "mcp__satellites__document_get",
		"tool_input":        map[string]any{"id": "sty_abc123"},
	}
	var out bytes.Buffer
	if err := runHookAccess(jsonReader(t, payload), &out, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Errorf("subagent should inherit the parent's engagement (no reminder), got: %s", out.String())
	}
	// And no stray candidate was opened under the child session.
	store, _ = workstate.Open(storePath(repo))
	defer store.Close()
	if _, ok, _ := store.Current("childSess", "sty_abc123"); ok {
		t.Errorf("a stray candidate was opened under the child session")
	}
}

// TestSessionKey: parent wins when present, else own session.
func TestSessionKey(t *testing.T) {
	if got := sessionKey("child", "parent"); got != "parent" {
		t.Errorf("sessionKey(child,parent) = %q, want parent", got)
	}
	if got := sessionKey("child", ""); got != "child" {
		t.Errorf("sessionKey(child,\"\") = %q, want child", got)
	}
	if got := sessionKey("child", "  "); got != "child" {
		t.Errorf("sessionKey with blank parent = %q, want child", got)
	}
}

// TestHookAccess_FailsOpenWhenUnconfigured: outside a satellites repo the
// advisory does nothing and never errors (fail-open).
func TestHookAccess_FailsOpenWhenUnconfigured(t *testing.T) {
	dir := t.TempDir() // no .satellites/
	payload := map[string]any{
		"session_id": "sessA", "cwd": dir,
		"tool_name": "mcp__satellites__document_get", "tool_input": map[string]any{"id": "sty_abc123"},
	}
	var out bytes.Buffer
	if err := runHookAccess(jsonReader(t, payload), &out, time.Now().UTC()); err != nil {
		t.Fatalf("unconfigured repo should not error: %v", err)
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Errorf("unconfigured repo should be silent, got: %s", out.String())
	}
}
