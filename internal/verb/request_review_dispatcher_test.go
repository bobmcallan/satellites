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

// TestParseGateOutput_BraceBearingProse pins the sty_756ad5f3 fix: the gate
// emitted paragraphs of reasoning containing a `{skills,documents,...}`
// token before its decision JSON. The old parser locked onto that first `{`
// and threw the real verdict away on a parse error. The decision — and its
// notes — must survive prose that carries braces.
func TestParseGateOutput_BraceBearingProse(t *testing.T) {
	raw := []byte(`I reviewed the change. The skills live under
config/wksp_6f048cd8/proj_fc7d72d8/{skills,documents,principles}/ and
AC4 requires .claude/skills/ to be gitignored, which it is not.

{"decision": "reject", "notes": "AC4 unmet: .claude/skills/ is not gitignored"}
`)
	out, err := ParseGateOutput(raw)
	if err != nil {
		t.Fatalf("ParseGateOutput on brace-bearing prose: %v", err)
	}
	if out.Decision != GateDecisionReject {
		t.Fatalf("decision = %q, want %q", out.Decision, GateDecisionReject)
	}
	if !strings.Contains(out.Notes, "AC4 unmet") {
		t.Fatalf("notes lost: %q", out.Notes)
	}
}

// TestParseGateOutput_TrailingProseAfterDecision covers a brace appearing in
// prose *after* the decision too — taking the last valid decision object
// must not be confused by a later `{...}` that is not a verdict.
func TestParseGateOutput_TrailingProseAfterDecision(t *testing.T) {
	raw := []byte(`Reasoning first.
{"decision": "accept", "notes": "all ACs met"}
Note: see config/{a,b}/ for details.`)
	out, err := ParseGateOutput(raw)
	if err != nil {
		t.Fatalf("ParseGateOutput: %v", err)
	}
	if out.Decision != GateDecisionAccept {
		t.Fatalf("decision = %q, want %q", out.Decision, GateDecisionAccept)
	}
	if out.Notes != "all ACs met" {
		t.Fatalf("notes = %q, want %q", out.Notes, "all ACs met")
	}
}

// TestParseGateOutput_CleanJSON keeps the bare-object path working (AC4).
func TestParseGateOutput_CleanJSON(t *testing.T) {
	out, err := ParseGateOutput([]byte(`{"decision":"accept","notes":"ok"}`))
	if err != nil {
		t.Fatalf("ParseGateOutput on clean JSON: %v", err)
	}
	if out.Decision != GateDecisionAccept || out.Notes != "ok" {
		t.Fatalf("got %+v, want accept/ok", out)
	}
}

// TestParseGateOutput_CodeFenced keeps the ```json-fenced path working (AC4).
func TestParseGateOutput_CodeFenced(t *testing.T) {
	raw := []byte("```json\n{\"decision\":\"reject\",\"notes\":\"missing test\"}\n```")
	out, err := ParseGateOutput(raw)
	if err != nil {
		t.Fatalf("ParseGateOutput on code-fenced JSON: %v", err)
	}
	if out.Decision != GateDecisionReject || out.Notes != "missing test" {
		t.Fatalf("got %+v, want reject/missing test", out)
	}
}

// TestParseGateOutput_NoDecision errors rather than silently accepting when
// the output carries no valid decision object (AC3) — including the case
// where prose has braces but no decision JSON at all.
func TestParseGateOutput_NoDecision(t *testing.T) {
	cases := map[string][]byte{
		"prose only":          []byte("I could not reach a decision."),
		"brace prose only":    []byte("paths are config/{skills,documents}/ here"),
		"empty":               []byte(""),
		"object wrong field":  []byte(`{"verdict":"accept"}`),
		"decision out of set": []byte(`{"decision":"maybe","notes":"unsure"}`),
		"unbalanced brace":    []byte(`{"decision": "accept"`),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := ParseGateOutput(raw)
			if err == nil {
				t.Fatalf("expected error, got decision %q", out.Decision)
			}
			if out.Decision != "" {
				t.Fatalf("decision must be empty on error, got %q", out.Decision)
			}
		})
	}
}
