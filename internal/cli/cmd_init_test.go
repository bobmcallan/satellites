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
func TestRunInit_ConsumptionConfigAndIdempotent(t *testing.T) {
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
	// init configures CONSUMPTION for skills/documents (no local authoring), and
	// the toml carries the documented library_pins block. But the ORDER-ZERO
	// substrate IS scaffolded (epic:satellites-backbone 2.4): the baseline
	// workflow and a starter constitution under .satellites/principles.
	for _, sub := range []string{"skills", "documents"} {
		if _, err := os.Stat(filepath.Join(repo, ".satellites", sub)); !os.IsNotExist(err) {
			t.Errorf("init must NOT scaffold .satellites/%s (got err=%v)", sub, err)
		}
	}
	if _, err := os.Stat(filepath.Join(repo, ".satellites", "principles", "constitution.md")); err != nil {
		t.Errorf("init must scaffold the starter constitution .satellites/principles/constitution.md: %v", err)
	}
	if !strings.Contains(string(tomlBefore), "library_pins") {
		t.Errorf("fresh toml should carry the library_pins consumption block, got:\n%s", tomlBefore)
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

// TestEnsureGitignore covers the managed-block contract (sty_b47c758c): a fresh
// repo gets a .gitignore with every local-state pattern; an existing .gitignore
// gains the block once with the user's own lines preserved; a re-run is a no-op.
func TestEnsureGitignore(t *testing.T) {
	// Allowlist shape: ignore all of .satellites/* (covers state.db*/index.db*/
	// logs/work/worktree — the local-state set the AC names), then re-include the
	// committable authoring dirs + toml.
	wantPatterns := []string{
		".satellites/*",
		"!.satellites/documents/",
		"!.satellites/workflows/",
		"!.satellites/satellites.toml",
	}

	// Absent → created with all patterns.
	repo := t.TempDir()
	added, err := ensureGitignore(repo)
	if err != nil || !added {
		t.Fatalf("fresh ensureGitignore: added=%v err=%v, want added=true", added, err)
	}
	gi := filepath.Join(repo, ".gitignore")
	body, _ := os.ReadFile(gi)
	for _, p := range wantPatterns {
		if !strings.Contains(string(body), p) {
			t.Errorf("fresh .gitignore missing pattern %q\n%s", p, body)
		}
	}

	// Re-run → idempotent no-op.
	added2, err := ensureGitignore(repo)
	if err != nil {
		t.Fatalf("re-run err: %v", err)
	}
	if added2 {
		t.Errorf("re-run wrote the block again — must be idempotent")
	}
	body2, _ := os.ReadFile(gi)
	if n := strings.Count(string(body2), gitignoreMarker); n != 1 {
		t.Errorf("marker appears %d times after re-run, want 1", n)
	}

	// Existing user .gitignore → block appended once, user lines preserved.
	repo2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo2, ".gitignore"), []byte("node_modules/\n*.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if added, err := ensureGitignore(repo2); err != nil || !added {
		t.Fatalf("append into existing: added=%v err=%v", added, err)
	}
	merged, _ := os.ReadFile(filepath.Join(repo2, ".gitignore"))
	if !strings.Contains(string(merged), "node_modules/") || !strings.Contains(string(merged), "*.log") {
		t.Errorf("user .gitignore lines were lost:\n%s", merged)
	}
	if !strings.Contains(string(merged), ".satellites/*") || !strings.Contains(string(merged), gitignoreMarker) {
		t.Errorf("managed block not appended to existing .gitignore:\n%s", merged)
	}
	if added, _ := ensureGitignore(repo2); added {
		t.Errorf("second append into existing — must be idempotent")
	}
}

// TestScaffoldToml_BindReady: a fresh toml carries an ACTIVE server_url default
// (not the commented placeholder), so `satellites auth` no longer fails with
// `server_url not set` on a fresh install→init (sty_1dc6b146).
func TestScaffoldToml_BindReady(t *testing.T) {
	if tomlActiveKey([]byte(scaffoldToml), "server_url") == false {
		t.Fatalf("scaffoldToml must set an ACTIVE server_url; got:\n%s", scaffoldToml)
	}
	if !strings.Contains(scaffoldToml, `server_url = "`+defaultServerURL+`"`) {
		t.Errorf("scaffoldToml missing active default server_url %q", defaultServerURL)
	}
	if strings.Contains(scaffoldToml, `# server_url = "https://your-satellites-server"`) {
		t.Errorf("scaffoldToml still carries the stale commented server_url placeholder")
	}
	// project_id stays a commented placeholder until init resolves it via match.
	if tomlActiveKey([]byte(scaffoldToml), "project_id") {
		t.Errorf("scaffoldToml must NOT pre-set an active project_id (it is resolved via match)")
	}
}

// TestActivateTomlKey covers the three paths: upgrade a commented placeholder in
// place, append when no placeholder exists, and no-op when an active key is
// already present (idempotent re-run).
func TestActivateTomlKey(t *testing.T) {
	// 1. Upgrade a commented placeholder in place.
	repo := t.TempDir()
	p := filepath.Join(repo, "c.toml")
	if err := os.WriteFile(p, []byte("# server_url = \"https://your-satellites-server\"\n# other = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if added, err := activateTomlKey(p, "server_url", defaultServerURL); err != nil || !added {
		t.Fatalf("upgrade: added=%v err=%v, want added=true", added, err)
	}
	body, _ := os.ReadFile(p)
	if !strings.Contains(string(body), `server_url = "`+defaultServerURL+`"`) || strings.Contains(string(body), "# server_url") {
		t.Errorf("placeholder not upgraded in place:\n%s", body)
	}
	if !strings.Contains(string(body), "# other = 1") {
		t.Errorf("unrelated line lost:\n%s", body)
	}
	// 2. Idempotent: re-run is a no-op now that the key is active.
	if added, err := activateTomlKey(p, "server_url", defaultServerURL); err != nil || added {
		t.Errorf("re-run: added=%v err=%v, want added=false (idempotent)", added, err)
	}

	// 3. Append when no placeholder exists.
	p2 := filepath.Join(repo, "empty.toml")
	if err := os.WriteFile(p2, []byte("name = \"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if added, err := activateTomlKey(p2, "project_id", "proj_abc"); err != nil || !added {
		t.Fatalf("append: added=%v err=%v, want added=true", added, err)
	}
	b2, _ := os.ReadFile(p2)
	if !tomlActiveKey(b2, "project_id") || !strings.Contains(string(b2), `project_id = "proj_abc"`) {
		t.Errorf("project_id not appended active:\n%s", b2)
	}
}

// TestEnsureProjectID_PresentNoOp: an existing active project_id is reported
// present and the toml is left untouched (no match call, no rewrite).
func TestEnsureProjectID_PresentNoOp(t *testing.T) {
	repo := t.TempDir()
	p := filepath.Join(repo, "satellites.toml")
	in := "server_url = \"https://x\"\nproject_id = \"proj_keep\"\n"
	if err := os.WriteFile(p, []byte(in), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	ensureProjectID(&out, repo, p)
	after, _ := os.ReadFile(p)
	if string(after) != in {
		t.Errorf("toml rewritten despite active project_id:\n%s", after)
	}
	if !strings.Contains(out.String(), "already present") {
		t.Errorf("expected a present report, got: %s", out.String())
	}
}

// TestEnsureMCPJSON covers the .mcp.json contract (sty_cd83072f): create when
// absent, preserve an identical entry (no rewrite), reconcile a divergent url to
// the toml's server, keep other servers + unknown keys, and never duplicate.
func TestEnsureMCPJSON(t *testing.T) {
	const server = "https://satellites-pprod.fly.dev"
	wantURL := server + "/mcp"

	// 1. Absent → created with the satellites entry.
	repo := t.TempDir()
	st, err := ensureMCPJSON(repo, server)
	if err != nil || st != "added" {
		t.Fatalf("create: st=%q err=%v, want added", st, err)
	}
	p := filepath.Join(repo, ".mcp.json")
	var doc map[string]any
	b, _ := os.ReadFile(p)
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf(".mcp.json invalid: %v\n%s", err, b)
	}
	sat := doc["mcpServers"].(map[string]any)["satellites"].(map[string]any)
	if sat["url"] != wantURL || sat["type"] != "http" {
		t.Errorf("entry = %v, want {type:http, url:%s}", sat, wantURL)
	}

	// 2. Identical → preserved, no rewrite.
	before, _ := os.ReadFile(p)
	st2, err := ensureMCPJSON(repo, server)
	if err != nil || st2 != "" {
		t.Errorf("idempotent: st=%q err=%v, want no-op", st2, err)
	}
	after, _ := os.ReadFile(p)
	if string(before) != string(after) {
		t.Errorf(".mcp.json rewritten on identical re-run")
	}

	// 3. Divergent url + an unrelated server → reconciled, other server kept.
	repo2 := t.TempDir()
	seed := `{"mcpServers":{"satellites":{"type":"http","url":"https://OLD/mcp"},"other":{"type":"http","url":"https://x/mcp"}},"extra":true}`
	if err := os.WriteFile(filepath.Join(repo2, ".mcp.json"), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	st3, err := ensureMCPJSON(repo2, server)
	if err != nil || st3 != "reconciled" {
		t.Fatalf("reconcile: st=%q err=%v, want reconciled", st3, err)
	}
	var doc2 map[string]any
	b2, _ := os.ReadFile(filepath.Join(repo2, ".mcp.json"))
	_ = json.Unmarshal(b2, &doc2)
	servers := doc2["mcpServers"].(map[string]any)
	if servers["satellites"].(map[string]any)["url"] != wantURL {
		t.Errorf("divergent url not reconciled to %s: %v", wantURL, servers["satellites"])
	}
	if _, ok := servers["other"]; !ok {
		t.Errorf("unrelated MCP server `other` was dropped")
	}
	if doc2["extra"] != true {
		t.Errorf("unknown top-level key `extra` was dropped")
	}
}

// TestRunInit_WritesMCPJSON: a full init in a temp repo registers the satellites
// MCP server in .mcp.json from the scaffolded server_url default.
func TestRunInit_WritesMCPJSON(t *testing.T) {
	repo := t.TempDir()
	var out bytes.Buffer
	if err := runInit(&out, repo); err != nil {
		t.Fatalf("init: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(repo, ".mcp.json"))
	if err != nil {
		t.Fatalf(".mcp.json not written: %v", err)
	}
	if !strings.Contains(string(b), defaultServerURL+"/mcp") {
		t.Errorf(".mcp.json missing %s/mcp:\n%s", defaultServerURL, b)
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
	if !commandUnderEvent(t, s, "PreToolUse", commitGateMatcher, commitGateCommand) {
		t.Errorf("commit-gate hook (PreToolUse %s → %s) not installed", commitGateMatcher, commitGateCommand)
	}
	if !commandUnderEvent(t, s, "UserPromptSubmit", "", promptCommand) {
		t.Errorf("prompt reminder hook (UserPromptSubmit → %s) not installed", promptCommand)
	}
	if !commandUnderEvent(t, s, "PreToolUse", codeNudgeMatcher, codeNudgeCommand) {
		t.Errorf("code-search nudge hook (PreToolUse %s → %s) not installed", codeNudgeMatcher, codeNudgeCommand)
	}
	if !commandUnderEvent(t, s, "SessionStart", "", sessionIndexCommand) {
		t.Errorf("code-index refresh hook (SessionStart → %s) not installed", sessionIndexCommand)
	}
	if !commandUnderEvent(t, s, "SessionStart", "", sessionContextCommand) {
		t.Errorf("always-context inject hook (SessionStart → %s) not installed", sessionContextCommand)
	}
	if !commandUnderEvent(t, s, "Stop", "", stopCheckCommand) {
		t.Errorf("commit-without-gate Stop advisory (Stop → %s) not installed", stopCheckCommand)
	}
	if !commandUnderEvent(t, s, "PreToolUse", accessMatcher, sessionContextCommand) {
		t.Errorf("always-context re-anchor hook (PreToolUse %s → %s) not installed", accessMatcher, sessionContextCommand)
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
	if pre, _ := hooks["PreToolUse"].([]any); len(pre) != 5 { // door + commit-gate + access + code-nudge + always-context re-anchor
		t.Errorf("PreToolUse has %d entries after re-init, want 5 (door + commit-gate + access + code-nudge + always-context)", len(pre))
	}
	if ups, _ := hooks["UserPromptSubmit"].([]any); len(ups) != 1 {
		t.Errorf("UserPromptSubmit has %d entries after re-init, want 1", len(ups))
	}
	if ss, _ := hooks["SessionStart"].([]any); len(ss) != 2 { // code-index + always-context
		t.Errorf("SessionStart has %d entries after re-init, want 2 (code-index + always-context, no duplicate)", len(ss))
	}
}

// TestGitignoreBlock_LocalOverlayIgnored pins sty_f0c02276 AC3: the managed
// .gitignore allowlist re-includes the committed satellites.toml but NOT the
// per-user satellites.local.toml overlay, so the overlay (which may carry an
// api_key) stays gitignored — the secret-free-committed invariant holds.
func TestGitignoreBlock_LocalOverlayIgnored(t *testing.T) {
	if !strings.Contains(gitignoreBlock, ".satellites/*") {
		t.Fatal("gitignore block must ignore .satellites/* (the allowlist base)")
	}
	if !strings.Contains(gitignoreBlock, "!.satellites/satellites.toml") {
		t.Error("committed satellites.toml must be re-included (shared config is committed)")
	}
	if strings.Contains(gitignoreBlock, "satellites.local.toml") {
		t.Error("satellites.local.toml must NOT be re-included — the per-user overlay stays gitignored")
	}
}
