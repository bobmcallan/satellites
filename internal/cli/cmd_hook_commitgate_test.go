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
