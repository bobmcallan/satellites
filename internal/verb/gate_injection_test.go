package verb

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestEmbeddedGateInjectedAndUsed is the ENSURE control (epic:system-substrate):
// it proves that a config/skills reviewer — one that ships embedded in the
// binary and is absent from .claude/skills — is actually injected into the
// `claude -p` invocation AND used: its rubric body and its functional-check
// result both reach the subprocess, and a verdict flows back. A reviewer that
// shipped embedded but was never delivered to the agent would be a dead spine;
// this test forecloses that.
//
// It is load-bearing on the embed: the reviewer resolves via configSkillRaw (the
// binary). Remove satellites-selfcheck-review from config/skills and
// resolveGateSkill falls through to the (absent) worktree copy — Dispatch then
// errors and this test fails. The proof uses the sanctioned BinaryPath shim
// rather than a live `claude` (which cannot authenticate in CI).
func TestEmbeddedGateInjectedAndUsed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub claude is a POSIX sh script")
	}

	// Guard: the reviewer must resolve from the config/skills embed — the
	// precondition the whole proof rests on.
	if _, ok := configSkillRaw("satellites-selfcheck-review"); !ok {
		t.Fatal("satellites-selfcheck-review is not embedded — the ENSURE invariant has no subject")
	}

	// A stub `claude` that asserts the embedded reviewer's BODY and its appended
	// functional-check result both arrived in --append-system-prompt before
	// returning a verdict. Accept only when both are present; otherwise reject —
	// so a dropped injection turns into a failing verdict, not a false pass.
	stub := filepath.Join(t.TempDir(), "claude-stub.sh")
	script := `#!/bin/sh
prev=""
prompt=""
for a in "$@"; do
  if [ "$prev" = "--append-system-prompt" ]; then prompt="$a"; fi
  prev="$a"
done
hasbody=0; hascheck=0
case "$prompt" in *"embedded-substrate self-check"*) hasbody=1;; esac
case "$prompt" in *"Functional check"*) hascheck=1;; esac
if [ "$hasbody" = 1 ] && [ "$hascheck" = 1 ]; then
  printf '{"decision":"accept","notes":"embedded reviewer body and functional-check result injected"}\n'
else
  printf '{"decision":"reject","notes":"injection failed: embedded body or functional-check missing from the prompt"}\n'
fi
`
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}

	// Worktree with NO .claude/skills copy of the reviewer: resolveGateSkill must
	// fall through to the config/skills embed and inject THAT. (Under local-WINS a
	// .claude/skills copy would be a deliberate override, so the embed path is
	// exercised precisely when no local copy exists.) go.mod satisfies the
	// reviewer's `test -f go.mod` functional check.
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "go.mod"), []byte("module stub\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	disp := ClaudeCLIGateDispatcher{BinaryPath: stub}
	out, err := disp.Dispatch(context.Background(), GateInput{
		SkillName:    "satellites-selfcheck-review",
		StoryID:      "sty_test",
		ProjectID:    "proj_test",
		WorkspaceID:  "wksp_test",
		StoryBody:    "## Workflow\n",
		StoryStatus:  "techdebt-review",
		WorktreeRoot: worktree,
	})
	if err != nil {
		t.Fatalf("Dispatch of embedded reviewer failed: %v", err)
	}
	if out.Decision != string(GateDecisionAccept) {
		t.Fatalf("verdict = %q (%s) — the stub only accepts when the embedded reviewer body AND its functional-check result reached claude -p; a non-accept means it was not injected/used", out.Decision, out.Notes)
	}
}
