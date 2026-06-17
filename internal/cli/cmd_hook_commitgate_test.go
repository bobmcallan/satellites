package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestClassifyGitCommand pins the command classifier (AC1/AC2/AC3): push is
// always gated, commit only under the strict knob, compound commands and
// path/global-option forms are handled, and read-only git / non-git is not gated.
func TestClassifyGitCommand(t *testing.T) {
	cases := []struct {
		name       string
		command    string
		gateCommit bool
		want       gitGateAction
	}{
		{"plain push", "git push", false, gitGatePush},
		{"push with args", "git push origin main", false, gitGatePush},
		{"commit ungated by default", "git commit -m x", false, gitGateNone},
		{"commit gated when strict", "git commit -m x", true, gitGateCommit},
		{"compound add commit push", "git add . && git commit -m x && git push", false, gitGatePush},
		{"compound commit only, strict", "git add -A && git commit -m x", true, gitGateCommit},
		{"compound commit only, default", "git add -A && git commit -m x", false, gitGateNone},
		{"commit-push skill form", "git commit -F msg.txt && git push", false, gitGatePush},
		{"absolute git path", "/usr/bin/git push", false, gitGatePush},
		{"global option before subcommand", "git -C /repo push", false, gitGatePush},
		{"c option with value then commit", "git -c user.name=x commit -m y", true, gitGateCommit},
		{"read-only status", "git status", true, gitGateNone},
		{"read-only log", "git log --oneline -5", true, gitGateNone},
		{"read-only diff", "git diff --cached", true, gitGateNone},
		{"fetch is not gated", "git fetch origin", true, gitGateNone},
		{"non-git command", "go test ./... && ls", true, gitGateNone},
		{"push only inside quoted echo is not a git invocation", `echo "remember to git push"`, false, gitGateNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classifyGitCommand(c.command, c.gateCommit); got != c.want {
				t.Errorf("classifyGitCommand(%q, gateCommit=%v) = %d, want %d", c.command, c.gateCommit, got, c.want)
			}
		})
	}
}

// TestRunHookCommitGate_DenyWithoutEngagement pins AC1: a `git push` with no
// lease-fresh editable engagement is denied with a valid PreToolUse decision.
func TestRunHookCommitGate_DenyWithoutEngagement(t *testing.T) {
	repo := writeRepo(t, true, "")
	in := bytes.NewBufferString(`{"tool_name":"Bash","cwd":"` + repo + `","session_id":"sess1","tool_input":{"command":"git push"}}`)
	var out bytes.Buffer
	if err := runHookCommitGate(in, &out); err != nil {
		t.Fatalf("runHookCommitGate: %v", err)
	}
	var dec gateDecisionJSON
	if err := json.Unmarshal(out.Bytes(), &dec); err != nil {
		t.Fatalf("output not valid decision JSON: %v\n%s", err, out.String())
	}
	if dec.HookSpecificOutput.PermissionDecision != "deny" {
		t.Errorf("decision = %+v, want deny", dec.HookSpecificOutput)
	}
	if !strings.Contains(dec.HookSpecificOutput.PermissionDecisionReason, "git push") {
		t.Errorf("reason should name the blocked verb: %q", dec.HookSpecificOutput.PermissionDecisionReason)
	}
}

// TestRunHookCommitGate_AllowWithEngagement pins the allow path: a lease-fresh
// editable engagement for the session lets `git push` through (no output).
func TestRunHookCommitGate_AllowWithEngagement(t *testing.T) {
	repo := writeRepo(t, true, "")
	seedEngagement(t, repo, "sess1", "sty_live", "in_progress", true, time.Now().UTC().Add(time.Hour))
	in := bytes.NewBufferString(`{"tool_name":"Bash","cwd":"` + repo + `","session_id":"sess1","tool_input":{"command":"git push"}}`)
	var out bytes.Buffer
	if err := runHookCommitGate(in, &out); err != nil {
		t.Fatalf("runHookCommitGate: %v", err)
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Errorf("editable engagement should allow push (no output), got %q", out.String())
	}
}

// TestRunHookCommitGate_NonGitAllows pins that non-git Bash is never gated, even
// with no engagement at all.
func TestRunHookCommitGate_NonGitAllows(t *testing.T) {
	repo := writeRepo(t, true, "")
	in := bytes.NewBufferString(`{"tool_name":"Bash","cwd":"` + repo + `","session_id":"sess1","tool_input":{"command":"go build ./..."}}`)
	var out bytes.Buffer
	if err := runHookCommitGate(in, &out); err != nil {
		t.Fatalf("runHookCommitGate: %v", err)
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Errorf("non-git Bash must not be gated, got %q", out.String())
	}
}

// bashCommitGateEvent marshals a PreToolUse Bash event, avoiding hand-escaping a
// command that itself contains quotes/newlines.
func bashCommitGateEvent(t *testing.T, repo, session, command string) *bytes.Buffer {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"tool_name":  "Bash",
		"cwd":        repo,
		"session_id": session,
		"tool_input": map[string]any{"command": command},
	})
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewBuffer(b)
}

// TestRunHookCommitGate_QuotedOperatorsAllow pins sty_54c65577: a read-only Bash
// command whose only shell-operator characters (`>`, `|`, `;`) sit inside a
// quoted span must NOT be gated, even with no engagement. The regression case is
// the exact shape that falsely denied in session 7ba2a9aa — a `python3 -c` JSON
// reader containing the literal `'<root>'`.
func TestRunHookCommitGate_QuotedOperatorsAllow(t *testing.T) {
	repo := writeRepo(t, true, "")
	cases := []struct {
		name    string
		command string
	}{
		{"python -c json reader with '<root>' literal", "python3 -c \"\nimport json\nd=json.load(open('/etc/hostname'))\ndef walk(o,path=''):\n    if isinstance(o,dict):\n        print('container:', path or '<root>')\nwalk(d)\n\""},
		{"reported first-attempt form with 2>&1 | grep", "python3 -c \"print('<root>')\" 2>&1 | grep -i x | head"},
		{"redirect operator only inside echo string", "echo '> not a redirect; rm -rf /'"},
		{"separators only inside an interpreter body", "python3 -c \"print('a|b;c'); mv=1\""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := bashCommitGateEvent(t, repo, "sess1", c.command)
			var out bytes.Buffer
			if err := runHookCommitGate(in, &out); err != nil {
				t.Fatalf("runHookCommitGate: %v", err)
			}
			if strings.TrimSpace(out.String()) != "" {
				t.Errorf("quoted operators must not be gated, got deny: %q", out.String())
			}
		})
	}
}

// TestRunHookCommitGate_RealRedirectStillDenies pins the no-regression half of
// sty_54c65577: a genuine in-repo output redirection / mutation with no
// engagement is still denied — including a quoted TARGET (the quote follows the
// operator, so it is a real redirect).
func TestRunHookCommitGate_RealRedirectStillDenies(t *testing.T) {
	repo := writeRepo(t, true, "")
	cases := []struct {
		name    string
		command string
	}{
		{"truncate redirect", "echo hi > note.txt"},
		{"append redirect", "echo hi >> note.txt"},
		{"quoted target redirect", `echo hi > "out file.txt"`},
		{"sed in place", "sed -i s/a/b/ src.go"},
		{"mv into repo", "mv a.txt b.txt"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := bashCommitGateEvent(t, repo, "sess1", c.command)
			var out bytes.Buffer
			if err := runHookCommitGate(in, &out); err != nil {
				t.Fatalf("runHookCommitGate: %v", err)
			}
			var dec gateDecisionJSON
			if err := json.Unmarshal(out.Bytes(), &dec); err != nil {
				t.Fatalf("real mutation should deny with decision JSON: %v\n%s", err, out.String())
			}
			if dec.HookSpecificOutput.PermissionDecision != "deny" {
				t.Errorf("real mutation %q should deny, got %+v", c.command, dec.HookSpecificOutput)
			}
		})
	}
}

// TestBashMutationTargets_QuoteAware unit-pins target extraction: operators
// inside quotes yield no target; real redirects/mutations still do.
func TestBashMutationTargets_QuoteAware(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    []string
	}{
		{"quoted gt literal", "python3 -c \"print('<root>')\"", nil},
		{"separators in quotes", "python3 -c \"x='a|b;c'; print(x)\"", nil},
		{"real redirect", "echo hi > note.txt", []string{"note.txt"}},
		{"quoted real target", `echo hi > "out file.txt"`, []string{"out file.txt"}},
		{"fd dup not a target", "go test ./... 2>&1", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := bashMutationTargets(c.command)
			if strings.Join(got, "\x00") != strings.Join(c.want, "\x00") {
				t.Errorf("bashMutationTargets(%q) = %v, want %v", c.command, got, c.want)
			}
		})
	}
}

// TestRunHookCommitGate_StrictKnobGatesCommit pins AC3: with commit_gate="commit"
// in the local toml, a bare `git commit` (no engagement) is denied; with the
// default it would pass.
func TestRunHookCommitGate_StrictKnobGatesCommit(t *testing.T) {
	repo := writeRepo(t, true, "")
	// Strict knob in the repo toml.
	tomlPath := filepath.Join(repo, ".satellites", "satellites.toml")
	if err := os.WriteFile(tomlPath, []byte("server_url=\"x\"\ncommit_gate=\"commit\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := bytes.NewBufferString(`{"tool_name":"Bash","cwd":"` + repo + `","session_id":"sess1","tool_input":{"command":"git commit -m x"}}`)
	var out bytes.Buffer
	if err := runHookCommitGate(in, &out); err != nil {
		t.Fatalf("runHookCommitGate: %v", err)
	}
	var dec gateDecisionJSON
	if err := json.Unmarshal(out.Bytes(), &dec); err != nil {
		t.Fatalf("strict commit should deny with decision JSON: %v\n%s", err, out.String())
	}
	if dec.HookSpecificOutput.PermissionDecision != "deny" || !strings.Contains(dec.HookSpecificOutput.PermissionDecisionReason, "git commit") {
		t.Errorf("strict commit decision = %+v, want deny naming git commit", dec.HookSpecificOutput)
	}

	// Sanity: with the default knob, the same commit is not gated (no output).
	if err := os.WriteFile(tomlPath, []byte("server_url=\"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	in2 := bytes.NewBufferString(`{"tool_name":"Bash","cwd":"` + repo + `","session_id":"sess1","tool_input":{"command":"git commit -m x"}}`)
	var out2 bytes.Buffer
	if err := runHookCommitGate(in2, &out2); err != nil {
		t.Fatalf("runHookCommitGate default: %v", err)
	}
	if strings.TrimSpace(out2.String()) != "" {
		t.Errorf("default knob must not gate git commit, got %q", out2.String())
	}
}
