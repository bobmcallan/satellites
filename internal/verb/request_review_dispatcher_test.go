package verb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGateClaudeArgs pins the claude argv a gate run constructs. The
// `--skill` flag does not exist in the claude CLI; sty_1312d692 replaced
// it with `--append-system-prompt`. This test fails if `--skill` ever
// returns or if the system prompt is dropped — catching the class of bug
// that shipped a gate that died at dispatch on every run.
func TestGateClaudeArgs(t *testing.T) {
	args := gateClaudeArgs("GATE BODY")
	want := []string{"-p", "--append-system-prompt", "GATE BODY"}
	if len(args) != len(want) {
		t.Fatalf("argv length = %d, want %d (%v)", len(args), len(want), args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("argv[%d] = %q, want %q (full %v)", i, args[i], want[i], args)
		}
	}
	for _, a := range args {
		if a == "--skill" {
			t.Fatalf("argv must not contain the non-existent --skill flag: %v", args)
		}
	}
}

// TestResolveGateSkillBody reads .claude/skills/<name>/SKILL.md from the
// worktree and returns the body with frontmatter stripped.
func TestResolveGateSkillBody(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, ".claude", "skills", "done-review")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := "---\nname: done-review\ntype: skill\ntags: [kind:gate]\n---\nYou are the done-review gate. Emit decision JSON.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	body, err := resolveGateSkillBody(root, "done-review")
	if err != nil {
		t.Fatalf("resolveGateSkillBody: %v", err)
	}
	if strings.Contains(body, "---") || strings.Contains(body, "name: done-review") {
		t.Fatalf("frontmatter not stripped from gate body: %q", body)
	}
	if !strings.Contains(body, "You are the done-review gate") {
		t.Fatalf("gate body missing rubric: %q", body)
	}
}

// TestResolveGateSkillBody_Missing surfaces a clear error when the gate
// skill is absent from the worktree rather than dispatching an empty
// system prompt.
func TestResolveGateSkillBody_Missing(t *testing.T) {
	_, err := resolveGateSkillBody(t.TempDir(), "nonexistent-gate")
	if err == nil || !strings.Contains(err.Error(), "read gate skill") {
		t.Fatalf("expected read-gate-skill error, got %v", err)
	}
}
