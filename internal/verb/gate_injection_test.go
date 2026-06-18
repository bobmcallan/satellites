package verb

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestEmbeddedGateInjectedAndUsed is the ENSURE control (epic:minimal-spine
// order-4): it proves that an embedded internal gate — one that ships in the
// binary and is NEVER materialised to .claude/skills — is actually injected
// into the `claude -p` invocation AND used: its rubric body and its
// functional-check result both reach the subprocess, and a verdict flows back.
// A gate that shipped embedded but was never delivered to the agent would be a
// dead spine; this test forecloses that.
//
// It is load-bearing on the embed: the gate resolves via internalGateRaw (the
// binary). Remove satellites-internal-selfcheck from internalgates/ and
// resolveGateSkill falls through to the (absent) worktree copy — Dispatch then
// errors and this test fails. The proof uses the sanctioned BinaryPath shim
// rather than a live `claude` (which cannot authenticate in CI).
func TestEmbeddedGateInjectedAndUsed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub claude is a POSIX sh script")
	}

	// Guard: the gate must resolve from the binary embed, not the worktree —
	// the precondition the whole proof rests on.
	if _, ok := internalGateRaw("satellites-internal-selfcheck"); !ok {
		t.Fatal("satellites-internal-selfcheck is not embedded — the ENSURE invariant has no subject")
	}

	// A stub `claude` that asserts the embedded gate's BODY and its appended
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
case "$prompt" in *"satellites-INTERNAL gate"*) hasbody=1;; esac
case "$prompt" in *"Functional check"*) hascheck=1;; esac
if [ "$hasbody" = 1 ] && [ "$hascheck" = 1 ]; then
  printf '{"decision":"accept","notes":"embedded gate body and functional-check result both injected into claude -p"}\n'
else
  printf '{"decision":"reject","notes":"gate body or functional-check result missing from the claude -p prompt"}\n'
fi
`
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}

	// Worktree with go.mod (so the selfcheck `test -f go.mod` check passes) but
	// NO .claude/skills/satellites-internal-selfcheck — the gate can ONLY come
	// from the binary embed.
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "go.mod"), []byte("module stub\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if _, err := os.Stat(filepath.Join(worktree, ".claude", "skills", "satellites-internal-selfcheck")); !os.IsNotExist(err) {
		t.Fatalf("precondition: the gate must be absent from the worktree .claude/skills")
	}

	disp := ClaudeCLIGateDispatcher{BinaryPath: stub}
	out, err := disp.Dispatch(context.Background(), GateInput{
		SkillName:    "satellites-internal-selfcheck",
		StoryID:      "sty_test",
		ProjectID:    "proj_test",
		WorkspaceID:  "wksp_test",
		StoryBody:    "## Workflow\n",
		StoryStatus:  "techdebt-review",
		WorktreeRoot: worktree,
	})
	if err != nil {
		t.Fatalf("Dispatch of embedded gate failed: %v", err)
	}
	if out.Decision != string(GateDecisionAccept) {
		t.Fatalf("verdict = %q (%s) — the stub only accepts when the embedded gate body AND its functional-check result reached claude -p; a non-accept means the gate was not injected/used", out.Decision, out.Notes)
	}
}
