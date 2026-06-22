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

// TestRunHookCommitGate_AllowWhenCommitReady pins the allow path (sty_e925ff09):
// a lease-fresh engagement AT its commit-push step (CommitReady) lets `git push`
// through (no output).
func TestRunHookCommitGate_AllowWhenCommitReady(t *testing.T) {
	repo := writeRepo(t, true, "")
	seedEngagementCommit(t, repo, "sess1", "sty_live", "shipping", true, true, time.Now().UTC().Add(time.Hour))
	in := bytes.NewBufferString(`{"tool_name":"Bash","cwd":"` + repo + `","session_id":"sess1","tool_input":{"command":"git push"}}`)
	var out bytes.Buffer
	if err := runHookCommitGate(in, &out); err != nil {
		t.Fatalf("runHookCommitGate: %v", err)
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Errorf("commit-ready engagement should allow push (no output), got %q", out.String())
	}
}

// TestRunHookCommitGate_DenyWhenEditableButNotCommitReady pins the core
// sty_e925ff09 binding: an engagement that is editable but NOT at its commit-push
// step (e.g. in_progress) denies `git push` — committing is authorised only AT
// the ship step, not at any editable phase.
func TestRunHookCommitGate_DenyWhenEditableButNotCommitReady(t *testing.T) {
	repo := writeRepo(t, true, "")
	seedEngagementCommit(t, repo, "sess1", "sty_live", "in_progress", true, false, time.Now().UTC().Add(time.Hour))
	in := bytes.NewBufferString(`{"tool_name":"Bash","cwd":"` + repo + `","session_id":"sess1","tool_input":{"command":"git push"}}`)
	var out bytes.Buffer
	if err := runHookCommitGate(in, &out); err != nil {
		t.Fatalf("runHookCommitGate: %v", err)
	}
	var dec gateDecisionJSON
	if err := json.Unmarshal(out.Bytes(), &dec); err != nil {
		t.Fatalf("editable-but-not-commit-ready push should deny with decision JSON: %v\n%s", err, out.String())
	}
	if dec.HookSpecificOutput.PermissionDecision != "deny" {
		t.Errorf("decision = %+v, want deny", dec.HookSpecificOutput)
	}
	if !strings.Contains(dec.HookSpecificOutput.PermissionDecisionReason, "commit-push step") {
		t.Errorf("reason should steer to the commit-push step: %q", dec.HookSpecificOutput.PermissionDecisionReason)
	}
}

// TestRunHookCommitGate_EditBashAllowedAtInProgress pins that the stricter
// commit rule does NOT tighten file-mutation gating: an obvious in-repo edit via
// Bash (output redirection) is still allowed at an editable in_progress phase,
// even though that phase is NOT commit-ready.
func TestRunHookCommitGate_EditBashAllowedAtInProgress(t *testing.T) {
	repo := writeRepo(t, true, "")
	seedEngagementCommit(t, repo, "sess1", "sty_live", "in_progress", true, false, time.Now().UTC().Add(time.Hour))
	in := bashCommitGateEvent(t, repo, "sess1", "echo hi > note.txt")
	var out bytes.Buffer
	if err := runHookCommitGate(in, &out); err != nil {
		t.Fatalf("runHookCommitGate: %v", err)
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Errorf("a file edit at an editable phase must stay allowed regardless of commit-readiness, got deny: %q", out.String())
	}
}

// TestRunHookCommitGate_ReadOnlyGitAllowsNotCommitReady pins that read-only git
// is never gated even when the engagement is editable-but-not-commit-ready.
func TestRunHookCommitGate_ReadOnlyGitAllowsNotCommitReady(t *testing.T) {
	repo := writeRepo(t, true, "")
	seedEngagementCommit(t, repo, "sess1", "sty_live", "in_progress", true, false, time.Now().UTC().Add(time.Hour))
	in := bashCommitGateEvent(t, repo, "sess1", "git status && git log --oneline -5")
	var out bytes.Buffer
	if err := runHookCommitGate(in, &out); err != nil {
		t.Fatalf("runHookCommitGate: %v", err)
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Errorf("read-only git must not be gated, got %q", out.String())
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

// TestStripHeredocs pins sty_8f68efc8: heredoc bodies, their closing delimiter
// lines, and here-string operands are removed (they are DATA), while the command
// text BEFORE a `<<` operator (a real command on that line) survives.
func TestStripHeredocs(t *testing.T) {
	cases := []struct {
		name    string
		command string
		// substrings that MUST be gone (heredoc/here-string data) ...
		absent []string
		// ... and substrings that MUST remain (real command before the operator).
		present []string
	}{
		{
			name:    "quoted-delimiter heredoc body dropped",
			command: "satellites exec document_upsert --json \"$(cat <<'EOF'\nbody text > .satellites/x and rm .claude/skills/y\nEOF\n)\"",
			absent:  []string{"> .satellites/x", "rm .claude/skills/y", "EOF"},
			present: []string{"document_upsert"},
		},
		{
			name:    "bare-delimiter heredoc body dropped",
			command: "read -r -d '' BODY <<EOF\n> .satellites/a\nsed -i s/a/b/ .claude/skills/z\nEOF",
			absent:  []string{".satellites/a", "sed -i", ".claude/skills/z"},
			present: []string{"read -r -d"},
		},
		{
			name:    "tab-indented <<- closing delimiter recognised",
			command: "cmd <<-END\n\tdata rm .satellites/q\n\tEND\nafter",
			absent:  []string{"rm .satellites/q"},
			present: []string{"cmd ", "after"},
		},
		{
			name:    "here-string operand dropped, command kept",
			command: "satellites exec foo <<< \"governed > .claude/skills/h\"",
			absent:  []string{".claude/skills/h"},
			present: []string{"satellites exec foo"},
		},
		{
			name:    "real mutation BEFORE the operator survives",
			command: "tee .satellites/real.txt <<EOF\njust data\nEOF",
			absent:  []string{"just data"},
			present: []string{"tee .satellites/real.txt"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := stripHeredocs(c.command)
			for _, a := range c.absent {
				if strings.Contains(got, a) {
					t.Errorf("stripHeredocs kept data %q:\nin:  %q\nout: %q", a, c.command, got)
				}
			}
			for _, p := range c.present {
				if !strings.Contains(got, p) {
					t.Errorf("stripHeredocs dropped real command %q:\nin:  %q\nout: %q", p, c.command, got)
				}
			}
		})
	}
}

// TestRunHookCommitGate_HeredocDataAllows pins sty_8f68efc8 AC1/AC2: a Bash
// command that carries governed-path + redirect/mutation text only as DATA (a
// heredoc body or here-string fed to a verb) is NOT denied, even with no
// engagement — the body is data, not a file operation. This is the false-deny
// that blocked creating/patching stories through the CLI.
func TestRunHookCommitGate_HeredocDataAllows(t *testing.T) {
	repo := writeRepo(t, true, "")
	cases := []struct {
		name    string
		command string
	}{
		{
			name:    "document_upsert heredoc body referencing governed paths",
			command: "satellites exec document_upsert --json \"$(cat <<'EOF'\nApproach: a real rm .claude/skills/x and > .satellites/y are described as data here\nEOF\n)\"",
		},
		{
			name:    "read into var via bare heredoc",
			command: "read -r -d '' BODY <<EOF\nsed -i s/a/b/ .claude/skills/gate.md\ntee .satellites/foo\nEOF\nsatellites exec document_upsert --json \"$BODY\"",
		},
		{
			name:    "here-string data with governed path",
			command: "satellites exec document_upsert --json <<< '{\"body\":\"rm .claude/skills/z and > .satellites/w\"}'",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := bashCommitGateEvent(t, repo, "sess1", c.command)
			var out bytes.Buffer
			if err := runHookCommitGate(in, &out); err != nil {
				t.Fatalf("runHookCommitGate: %v", err)
			}
			if strings.TrimSpace(out.String()) != "" {
				t.Errorf("governed-path text passed only as heredoc/here-string DATA must not be gated, got deny: %q", out.String())
			}
		})
	}
}

// TestRunHookCommitGate_RealMutationBeforeHeredocStillDenies pins the
// no-regression half of sty_8f68efc8: a genuine mutation written BEFORE a `<<`
// operator (its target is real, only the body is data) is still denied without
// an engagement — stripping the body must not blind the gate to the real op.
func TestRunHookCommitGate_RealMutationBeforeHeredocStillDenies(t *testing.T) {
	repo := writeRepo(t, true, "")
	cases := []string{
		"tee .satellites/real.txt <<EOF\njust data\nEOF",
		"cat <<EOF > .satellites/written\nbody\nEOF",
	}
	for _, command := range cases {
		t.Run(command[:10], func(t *testing.T) {
			in := bashCommitGateEvent(t, repo, "sess1", command)
			var out bytes.Buffer
			if err := runHookCommitGate(in, &out); err != nil {
				t.Fatalf("runHookCommitGate: %v", err)
			}
			var dec gateDecisionJSON
			if err := json.Unmarshal(out.Bytes(), &dec); err != nil {
				t.Fatalf("real mutation before heredoc should deny with decision JSON: %v\n%s", err, out.String())
			}
			if dec.HookSpecificOutput.PermissionDecision != "deny" {
				t.Errorf("real mutation %q should deny, got %+v", command, dec.HookSpecificOutput)
			}
		})
	}
}
