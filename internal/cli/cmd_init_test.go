package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// countGateHooks returns how many PreToolUse entries in a settings doc carry
// the START-door command — used to assert "added once, never duplicated".
func countGateHooks(t *testing.T, raw []byte) int {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("settings not valid json: %v", err)
	}
	hooks, _ := doc["hooks"].(map[string]any)
	pre, _ := hooks["PreToolUse"].([]any)
	n := 0
	for _, e := range pre {
		em, _ := e.(map[string]any)
		hl, _ := em["hooks"].([]any)
		for _, h := range hl {
			hm, _ := h.(map[string]any)
			if cmd, _ := hm["command"].(string); cmd == hookCommand {
				n++
			}
		}
	}
	return n
}

// TestMergeHookIntoSettings_FreshAndIdempotent: a fresh doc gains the hook;
// merging again is a no-op (added=false), so re-running init never duplicates.
func TestMergeHookIntoSettings_FreshAndIdempotent(t *testing.T) {
	out, added, err := mergeHookIntoSettings(nil, "PreToolUse", hookMatcher, hookCommand)
	if err != nil || !added {
		t.Fatalf("fresh merge: added=%v err=%v, want added=true", added, err)
	}
	if n := countGateHooks(t, out); n != 1 {
		t.Fatalf("fresh merge installed %d gate hooks, want 1", n)
	}

	out2, added2, err := mergeHookIntoSettings(out, "PreToolUse", hookMatcher, hookCommand)
	if err != nil {
		t.Fatalf("re-merge err: %v", err)
	}
	if added2 {
		t.Errorf("re-merge added the hook again — must be idempotent")
	}
	if n := countGateHooks(t, out2); n != 1 {
		t.Errorf("after re-merge there are %d gate hooks, want 1 (no duplicate)", n)
	}
}

// TestMergeHookIntoSettings_PreservesExisting: unrelated keys and a pre-existing
// hook are kept; our entry is appended, not substituted.
func TestMergeHookIntoSettings_PreservesExisting(t *testing.T) {
	existing := []byte(`{
  "permissions": {"allow": ["Bash(ls:*)"]},
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "echo other"}]}
    ]
  }
}`)
	out, added, err := mergeHookIntoSettings(existing, "PreToolUse", hookMatcher, hookCommand)
	if err != nil || !added {
		t.Fatalf("merge into existing: added=%v err=%v", added, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("merged settings invalid: %v", err)
	}
	// Unrelated key preserved.
	if _, ok := doc["permissions"]; !ok {
		t.Errorf("merge dropped the unrelated `permissions` key")
	}
	// Both the pre-existing Bash hook and our gate hook are present.
	hooks := doc["hooks"].(map[string]any)
	pre := hooks["PreToolUse"].([]any)
	if len(pre) != 2 {
		t.Fatalf("PreToolUse has %d entries, want 2 (existing + gate)", len(pre))
	}
	if n := countGateHooks(t, out); n != 1 {
		t.Errorf("gate hook count = %d, want 1", n)
	}
	if !strings.Contains(string(out), "echo other") {
		t.Errorf("pre-existing Bash hook was lost")
	}
}

// TestRunInit_ScaffoldsAndIsIdempotent exercises the full init over a temp repo:
// first run creates .satellites/, satellites.toml, and the settings hook;
// re-run preserves the toml verbatim and does not duplicate the hook.
func TestRunInit_ScaffoldsAndIsIdempotent(t *testing.T) {
	repo := t.TempDir()

	var out bytes.Buffer
	if err := runInit(&out, repo); err != nil {
		t.Fatalf("init run 1: %v", err)
	}
	// Files exist.
	if _, err := os.Stat(filepath.Join(repo, ".satellites")); err != nil {
		t.Errorf(".satellites/ not created: %v", err)
	}
	tomlPath := filepath.Join(repo, ".satellites", "satellites.toml")
	tomlBefore, err := os.ReadFile(tomlPath)
	if err != nil {
		t.Fatalf("satellites.toml not created: %v", err)
	}
	settingsPath := filepath.Join(repo, ".claude", "settings.json")
	s1, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("settings.json not created: %v", err)
	}
	if n := countGateHooks(t, s1); n != 1 {
		t.Fatalf("after init: %d gate hooks, want 1", n)
	}

	// Second run: idempotent.
	var out2 bytes.Buffer
	if err := runInit(&out2, repo); err != nil {
		t.Fatalf("init run 2: %v", err)
	}
	if !strings.Contains(out2.String(), "already present") {
		t.Errorf("re-run should report items already present, got:\n%s", out2.String())
	}
	tomlAfter, _ := os.ReadFile(tomlPath)
	if string(tomlAfter) != string(tomlBefore) {
		t.Errorf("satellites.toml was rewritten on re-run; must be left intact")
	}
	s2, _ := os.ReadFile(settingsPath)
	if n := countGateHooks(t, s2); n != 1 {
		t.Errorf("after re-init: %d gate hooks, want 1 (no duplicate)", n)
	}
}

// TestRunInit_LeavesExistingTomlAndSettings: an existing toml + settings.json
// with unrelated content survive init untouched except for the added hook.
func TestRunInit_LeavesExistingTomlAndSettings(t *testing.T) {
	repo := t.TempDir()
	sat := filepath.Join(repo, ".satellites")
	if err := os.MkdirAll(sat, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sat, "satellites.toml"), []byte("project_id=\"proj_keep\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	claude := filepath.Join(repo, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claude, "settings.json"), []byte(`{"model":"opus"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runInit(&out, repo); err != nil {
		t.Fatalf("init: %v", err)
	}
	toml, _ := os.ReadFile(filepath.Join(sat, "satellites.toml"))
	if !strings.Contains(string(toml), "proj_keep") {
		t.Errorf("existing satellites.toml was overwritten")
	}
	s, _ := os.ReadFile(filepath.Join(claude, "settings.json"))
	if !strings.Contains(string(s), `"model"`) {
		t.Errorf("existing settings key `model` was lost")
	}
	if n := countGateHooks(t, s); n != 1 {
		t.Errorf("gate hook not installed into existing settings (count=%d)", n)
	}
}

// commandUnderEvent reports whether a settings doc carries `command` under the
// named hook event (with the expected matcher, "" = any/none).
func commandUnderEvent(t *testing.T, raw []byte, event, matcher, command string) bool {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("settings not valid json: %v", err)
	}
	hooks, _ := doc["hooks"].(map[string]any)
	arr, _ := hooks[event].([]any)
	for _, e := range arr {
		em, _ := e.(map[string]any)
		if matcher != "" {
			if m, _ := em["matcher"].(string); m != matcher {
				continue
			}
		}
		hl, _ := em["hooks"].([]any)
		for _, h := range hl {
			hm, _ := h.(map[string]any)
			if cmd, _ := hm["command"].(string); cmd == command {
				return true
			}
		}
	}
	return false
}

// TestRunInit_InstallsAccessTriggers: init installs the advisory story-access
// hooks alongside the door — a PreToolUse hook on the MCP fetch and a
// UserPromptSubmit hook — and is idempotent across all three (AC6).
func TestRunInit_InstallsAccessTriggers(t *testing.T) {
	repo := t.TempDir()
	var out bytes.Buffer
	if err := runInit(&out, repo); err != nil {
		t.Fatalf("init: %v", err)
	}
	s, err := os.ReadFile(filepath.Join(repo, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("settings.json: %v", err)
	}
	if !commandUnderEvent(t, s, "PreToolUse", accessMatcher, accessCommand) {
		t.Errorf("access reminder hook (PreToolUse %s → %s) not installed", accessMatcher, accessCommand)
	}
	if !commandUnderEvent(t, s, "UserPromptSubmit", "", promptCommand) {
		t.Errorf("prompt reminder hook (UserPromptSubmit → %s) not installed", promptCommand)
	}
	if !commandUnderEvent(t, s, "PreToolUse", codeNudgeMatcher, codeNudgeCommand) {
		t.Errorf("code-search nudge hook (PreToolUse %s → %s) not installed", codeNudgeMatcher, codeNudgeCommand)
	}

	// Idempotent: a second init adds nothing and reports all present.
	var out2 bytes.Buffer
	if err := runInit(&out2, repo); err != nil {
		t.Fatalf("re-init: %v", err)
	}
	s2, _ := os.ReadFile(filepath.Join(repo, ".claude", "settings.json"))
	var doc map[string]any
	_ = json.Unmarshal(s2, &doc)
	hooks := doc["hooks"].(map[string]any)
	if pre, _ := hooks["PreToolUse"].([]any); len(pre) != 3 { // door + access + code-nudge
		t.Errorf("PreToolUse has %d entries after re-init, want 3 (door + access + code-nudge)", len(pre))
	}
	if ups, _ := hooks["UserPromptSubmit"].([]any); len(ups) != 1 {
		t.Errorf("UserPromptSubmit has %d entries after re-init, want 1", len(ups))
	}
}
