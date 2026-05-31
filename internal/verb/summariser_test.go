package verb

import (
	"strings"
	"testing"
)

// TestSummariserClaudeArgs pins the argv a step-summariser run constructs
// (sty_2517f6b8). The summariser runs `claude -p --append-system-prompt
// <skill-body>` with a READ-ONLY tool grant — unlike the gate it never
// builds or mutates the tree, so Bash must NOT be in its allowlist. This
// fails if the system prompt is dropped, the `--skill` non-flag returns, or
// the grant widens to include a write/exec tool.
func TestSummariserClaudeArgs(t *testing.T) {
	args := summariserClaudeArgs("SUMMARY BODY")
	want := []string{"-p", "--allowedTools", summariserAllowedTools, "--append-system-prompt", "SUMMARY BODY"}
	if len(args) != len(want) {
		t.Fatalf("argv length = %d, want %d (%v)", len(args), len(want), args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("argv[%d] = %q, want %q (full %v)", i, args[i], want[i], args)
		}
	}
	if strings.Contains(summariserAllowedTools, "Bash") {
		t.Fatalf("summariser tool grant must be read-only, got %q", summariserAllowedTools)
	}
	for _, a := range args {
		if a == "--skill" {
			t.Fatalf("argv must not contain the non-existent --skill flag: %v", args)
		}
	}
}

// TestSummarise_RequiresSkillName guards the empty-skill case — a misconfig
// (no step_summariser_skill) must error, not silently shell out.
func TestSummarise_RequiresSkillName(t *testing.T) {
	_, err := ClaudeCLISummariser{}.Summarise(nil, SummariserInput{})
	if err == nil || !strings.Contains(err.Error(), "skill_name required") {
		t.Fatalf("expected skill_name-required error, got %v", err)
	}
}
