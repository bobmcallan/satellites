package verb

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunGateCheck_ExitCodes pins the deterministic half of a gate: the
// harness runs the `check:` command in the worktree and reports its exit code
// and output faithfully — a pass is 0, a failure is the command's non-zero
// code, never silently 0 (epic:satellites-backbone 2.2 AC1).
func TestRunGateCheck_ExitCodes(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "marker"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	cases := []struct {
		name     string
		check    string
		wantCode int
		wantOut  string
	}{
		{"pass", "test -f marker && echo ok", 0, "ok"},
		{"fail-explicit", "echo nope; exit 3", 3, "nope"},
		{"fail-missing-file", "test -f absent", 1, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, out := runGateCheck(context.Background(), root, c.check, nil)
			if code != c.wantCode {
				t.Fatalf("exit = %d, want %d (out=%q)", code, c.wantCode, out)
			}
			if out != c.wantOut {
				t.Fatalf("out = %q, want %q", out, c.wantOut)
			}
		})
	}
}

// TestRunGateCheck_EnvInjection pins that the story context is folded into the
// check's environment, so a deterministic check (e.g. parent-close enumerating
// children) can act on the story it is gating via $SATELLITES_STORY_ID.
func TestRunGateCheck_EnvInjection(t *testing.T) {
	code, out := runGateCheck(context.Background(), "", `echo "id=$SATELLITES_STORY_ID status=$SATELLITES_STORY_STATUS"`,
		map[string]string{"SATELLITES_STORY_ID": "sty_abc123", "SATELLITES_STORY_STATUS": "backlog"})
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (out=%q)", code, out)
	}
	if out != "id=sty_abc123 status=backlog" {
		t.Fatalf("out = %q, want injected story context", out)
	}
}

// TestAppendFunctionalCheck_Injection pins that the deterministic result is
// folded into the gate's system prompt as a labelled section the gate body can
// read — and that the original rubric is preserved ahead of it.
func TestAppendFunctionalCheck_Injection(t *testing.T) {
	got := appendFunctionalCheck("RUBRIC BODY", "go build ./...", 2, "build failed: x")
	for _, want := range []string{"RUBRIC BODY", "## Functional check", "go build ./...", "exit code: 2", "build failed: x"} {
		if !strings.Contains(got, want) {
			t.Fatalf("injected prompt missing %q:\n%s", want, got)
		}
	}
	// Empty output omits the output fence but still carries the exit code.
	clean := appendFunctionalCheck("RUBRIC", "true", 0, "")
	if strings.Contains(clean, "output:") {
		t.Fatalf("empty output must not emit an output section:\n%s", clean)
	}
	if !strings.Contains(clean, "exit code: 0") {
		t.Fatalf("clean check must still report exit code 0:\n%s", clean)
	}
}

// TestResolveGateSkill_LocalWinsOverEmbed pins the operator's local-WINS model
// (epic:system-substrate): a config/skills reviewer is binary-resident, but a
// `.claude/skills/<name>` of the same name OVERRIDES the embed — the local copy
// is a deliberate, live override, not a dead shadow. With no local copy the
// embed is resolved.
func TestResolveGateSkill_LocalWinsOverEmbed(t *testing.T) {
	// 1. A local .claude/skills override WINS over the config/skills embed.
	root := t.TempDir()
	skillDir := filepath.Join(root, ".claude", "skills", "satellites-selfcheck-review")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	override := "---\nname: satellites-selfcheck-review\nkind: reviewer\ncheck: \"exit 1\"\n---\nLOCAL-OVERRIDE: this repo's edited copy wins over the embed.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(override), 0o644); err != nil {
		t.Fatalf("write override: %v", err)
	}
	fm, body, err := resolveGateSkill(root, "satellites-selfcheck-review")
	if err != nil {
		t.Fatalf("resolveGateSkill local: %v", err)
	}
	if !strings.Contains(body, "LOCAL-OVERRIDE") {
		t.Fatalf("local .claude/skills copy must WIN over the embed (local-WINS), got:\n%s", body)
	}
	if strings.TrimSpace(fm.Check) != "exit 1" {
		t.Fatalf("local override check = %q, want %q", fm.Check, "exit 1")
	}

	// 2. With NO local copy, the config/skills embed is resolved.
	empty := t.TempDir()
	fm2, body2, err := resolveGateSkill(empty, "satellites-selfcheck-review")
	if err != nil {
		t.Fatalf("resolveGateSkill embed: %v", err)
	}
	if !strings.Contains(body2, "embedded-substrate self-check") {
		t.Fatalf("embedded reviewer body not returned: %q", body2)
	}
	if strings.TrimSpace(fm2.Check) != "test -f go.mod" {
		t.Fatalf("embedded reviewer check = %q, want %q", fm2.Check, "test -f go.mod")
	}
}

// TestResolveGateSkill_WorktreeFallback keeps the ordinary path: a gate that is
// NOT internal is read from the worktree, and its `check:` frontmatter is
// parsed and surfaced.
func TestResolveGateSkill_WorktreeFallback(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, ".claude", "skills", "ordinary-gate")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := "---\nname: ordinary-gate\nkind: gate\ncheck: \"go build ./...\"\n---\nOrdinary worktree gate body.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	fm, body, err := resolveGateSkill(root, "ordinary-gate")
	if err != nil {
		t.Fatalf("resolveGateSkill worktree: %v", err)
	}
	if !strings.Contains(body, "Ordinary worktree gate body") {
		t.Fatalf("worktree gate body missing: %q", body)
	}
	if strings.TrimSpace(fm.Check) != "go build ./..." {
		t.Fatalf("check = %q, want %q", fm.Check, "go build ./...")
	}
}

// TestConfigSkillRaw_Registry confirms the config/skills embed carries the
// demonstration reviewer and reports a miss for an unknown name.
func TestConfigSkillRaw_Registry(t *testing.T) {
	if _, ok := configSkillRaw("satellites-selfcheck-review"); !ok {
		t.Fatal("expected satellites-selfcheck-review to be an embedded config/skills reviewer")
	}
	if _, ok := configSkillRaw("not-an-embedded-reviewer"); ok {
		t.Fatal("unknown name must not resolve as an embedded reviewer")
	}
}

// TestIsConfigSkill pins the public predicate the drift checks use to treat a
// binary-resident config/skills reviewer as available (epic:system-substrate).
func TestIsConfigSkill(t *testing.T) {
	// The default story reviewers (plan / done / cancel) and the selfcheck ship
	// embedded in config/skills so they govern any repo from the binary.
	for _, embedded := range []string{
		"satellites-intent-plan-review",
		"satellites-story-done-review",
		"satellites-story-cancel-review",
		"satellites-selfcheck-review",
	} {
		if !IsConfigSkill(embedded) {
			t.Fatalf("embedded config/skills reviewer %q must report IsConfigSkill true", embedded)
		}
	}
	// A name that is not in the config/skills embed reports false — including
	// intent-code-review, which resolves from .claude/skills, not the binary.
	for _, materialised := range []string{"satellites-story-plan-review", "satellites-intent-code-review"} {
		if IsConfigSkill(materialised) {
			t.Fatalf("non-embedded reviewer %q must report IsConfigSkill false", materialised)
		}
	}
}
