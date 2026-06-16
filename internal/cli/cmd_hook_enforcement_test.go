package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestBashMutationTargets pins the heuristic file-mutation detector (sty_448d2024
// AC2/AC3): the obvious in-repo write forms are extracted; reads, fd redirects,
// /dev/*, cp sources, and non-mutating commands are not.
func TestBashMutationTargets(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    []string
	}{
		{"stdout redirect", "echo x > foo.txt", []string{"foo.txt"}},
		{"append redirect", "echo x >> foo.txt", []string{"foo.txt"}},
		{"glued redirect", "echo x >foo.txt", []string{"foo.txt"}},
		{"redirect to /dev/null is ignored", "noisy > /dev/null", nil},
		{"stderr fd redirect is ignored", "cmd 2> err.log", nil},
		{"dup fd is ignored", "cmd > out 2>&1", []string{"out"}},
		{"tee writes its files", "echo x | tee a.txt b.txt", []string{"a.txt", "b.txt"}},
		{"tee append flag skipped", "echo x | tee -a log.txt", []string{"log.txt"}},
		{"mv both args", "mv a b", []string{"a", "b"}},
		{"cp only dest", "cp src dst", []string{"dst"}},
		{"cp recursive only dest", "cp -r srcdir dstdir", []string{"dstdir"}},
		{"rm all paths", "rm -rf x y", []string{"x", "y"}},
		{"sed in place", "sed -i 's/a/b/' file.txt", []string{"file.txt"}},
		{"sed in place with suffix", "sed -i.bak 's/a/b/' file.txt", []string{"file.txt"}},
		{"sed without in place is a read", "sed 's/a/b/' file.txt", nil},
		{"git mv both args", "git mv old new", []string{"old", "new"}},
		{"plain read", "cat file.txt", nil},
		{"grep read", "grep foo *.go", nil},
		{"build", "go build ./...", nil},
		{"compound pipe to tee", "cat a | tee b.txt", []string{"b.txt"}},
		{"compound rm after echo", "echo hi && rm c.txt", []string{"c.txt"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := bashMutationTargets(c.command)
			sort.Strings(got)
			want := append([]string(nil), c.want...)
			sort.Strings(want)
			if len(got) == 0 && len(want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("bashMutationTargets(%q) = %v, want %v", c.command, got, want)
			}
		})
	}
}

// TestGateOutcome_CrossRepo pins AC1: a mutation targeting ANOTHER governed repo
// is gated against THAT repo's engagement — not merely ungated for being outside
// this repo. A non-governed outside path stays ungated.
func TestGateOutcome_CrossRepo(t *testing.T) {
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(time.Hour)

	repoA := writeRepo(t, true, "") // cwd repo (the session works here)
	repoB := writeRepo(t, true, "") // a DIFFERENT governed repo
	targetInB := filepath.Join(repoB, "internal", "x.go")

	// 1. No engagement in B → a mutation into B is denied, naming B.
	if allow, reason := gateOutcome(repoA, "sess1", targetInB, now); allow || !strings.Contains(reason, repoB) {
		t.Errorf("cross-repo edit without an engagement in B must deny naming B: allow=%v reason=%q", allow, reason)
	}

	// 2. A lease-fresh editable engagement IN B (same session) → allowed.
	seedEngagement(t, repoB, "sess1", "sty_b", "in_progress", true, fresh)
	if allow, _ := gateOutcome(repoA, "sess1", targetInB, now); !allow {
		t.Errorf("cross-repo edit with a fresh editable engagement in B must be allowed")
	}

	// 3. An engagement in A does NOT authorise editing B.
	seedEngagement(t, repoA, "sess2", "sty_a", "in_progress", true, fresh)
	if allow, _ := gateOutcome(repoA, "sess2", targetInB, now); allow {
		t.Errorf("an engagement in A must not authorise a mutation into B")
	}

	// 4. A non-governed outside path (no .satellites) stays ungated.
	outside := filepath.Join(t.TempDir(), "claude-home", "memory", "note.md")
	if allow, _ := gateOutcome(repoA, "sess1", outside, now); !allow {
		t.Errorf("a non-governed outside path must stay ungated")
	}
}

// TestRunHookCommitGate_BashMutation pins AC2/AC3 end-to-end: an in-repo bash
// redirection is denied without an engagement and allowed with one, while a /tmp
// redirection and a read pass untouched (no false positives).
func TestRunHookCommitGate_BashMutation(t *testing.T) {
	repo := writeRepo(t, true, "")

	// In-repo redirection, no engagement → deny.
	mut := `{"tool_name":"Bash","cwd":"` + repo + `","session_id":"sess1","tool_input":{"command":"echo x > note.txt"}}`
	var out bytes.Buffer
	if err := runHookCommitGate(bytes.NewBufferString(mut), &out); err != nil {
		t.Fatalf("runHookCommitGate: %v", err)
	}
	var dec gateDecisionJSON
	if err := json.Unmarshal(out.Bytes(), &dec); err != nil {
		t.Fatalf("expected a deny decision, got %q (err %v)", out.String(), err)
	}
	if dec.HookSpecificOutput.PermissionDecision != "deny" {
		t.Errorf("in-repo bash mutation without engagement must deny, got %+v", dec.HookSpecificOutput)
	}

	// A /tmp redirection (outside any governed repo) → allow even with no engagement.
	tmp := filepath.Join(t.TempDir(), "scratch.txt")
	tmpCmd := `{"tool_name":"Bash","cwd":"` + repo + `","session_id":"sess1","tool_input":{"command":"echo x > ` + tmp + `"}}`
	out.Reset()
	if err := runHookCommitGate(bytes.NewBufferString(tmpCmd), &out); err != nil {
		t.Fatalf("runHookCommitGate: %v", err)
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Errorf("a /tmp redirection must not be gated, got %q", out.String())
	}

	// A read passes.
	out.Reset()
	readCmd := `{"tool_name":"Bash","cwd":"` + repo + `","session_id":"sess1","tool_input":{"command":"cat note.txt"}}`
	if err := runHookCommitGate(bytes.NewBufferString(readCmd), &out); err != nil {
		t.Fatalf("runHookCommitGate: %v", err)
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Errorf("a read command must not be gated, got %q", out.String())
	}

	// With a lease-fresh editable engagement → the mutation is allowed.
	seedEngagement(t, repo, "sess1", "sty_live", "in_progress", true, time.Now().UTC().Add(time.Hour))
	out.Reset()
	if err := runHookCommitGate(bytes.NewBufferString(mut), &out); err != nil {
		t.Fatalf("runHookCommitGate: %v", err)
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Errorf("an engaged session's in-repo mutation must be allowed, got %q", out.String())
	}
}
