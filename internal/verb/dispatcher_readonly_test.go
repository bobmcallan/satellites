package verb

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// writeArgvShim writes a fake `claude` that records each argv entry (one per
// line) to argvFile and prints a fixed accept verdict, so a test can assert the
// exact flags the dispatcher passed.
func writeArgvShim(t *testing.T, dir, argvFile string) string {
	t.Helper()
	shim := filepath.Join(dir, "claude")
	script := "#!/bin/sh\n: > '" + argvFile + "'\nfor a in \"$@\"; do printf '%s\\n' \"$a\" >> '" + argvFile + "'; done\nprintf '{\"decision\":\"accept\",\"notes\":\"ok\"}\\n'\n"
	if err := os.WriteFile(shim, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return shim
}

func allowedToolsArg(t *testing.T, argvFile string) string {
	t.Helper()
	raw, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("read argv: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	for i, a := range lines {
		if a == "--allowedTools" && i+1 < len(lines) {
			return lines[i+1]
		}
	}
	t.Fatalf("--allowedTools not found in argv:\n%s", raw)
	return ""
}

// TestDispatchReadOnlyWithholdsBash pins the validate guarantee (sty_0454b437):
// a ReadOnly dispatch grants only read tools — Bash (the gate's only enact
// channel: `satellites exec ledger_append`) is withheld, so the gate physically
// cannot change status/ledger — and the validate-only directive rides the prompt.
func TestDispatchReadOnlyWithholdsBash(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh shim not portable to windows")
	}
	dir := t.TempDir()
	argvFile := filepath.Join(dir, "argv.txt")
	shim := writeArgvShim(t, dir, argvFile)

	disp := ClaudeCLIGateDispatcher{BinaryPath: shim, ReadOnly: true}
	out, err := disp.Dispatch(context.Background(), GateInput{
		SkillName:   "satellites-task-upsert-review", // embedded → resolves with no local/server
		StoryID:     "tsk_x",
		StoryStatus: "ready",
		StoryBody:   "## Task\nx\n## Output\ny\n## Verification\nz",
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if out.Decision != GateDecisionAccept {
		t.Fatalf("decision = %q, want accept", out.Decision)
	}
	tools := allowedToolsArg(t, argvFile)
	if tools != readOnlyGateAllowedTools {
		t.Fatalf("ReadOnly --allowedTools = %q, want %q", tools, readOnlyGateAllowedTools)
	}
	if strings.Contains(tools, "Bash") {
		t.Fatalf("ReadOnly grant must withhold Bash (no enact); got %q", tools)
	}
	if raw, _ := os.ReadFile(argvFile); !strings.Contains(string(raw), "VALIDATE-ONLY") {
		t.Fatal("ReadOnly dispatch must append the validate-only directive to the prompt")
	}
}

// TestDispatchDefaultGrantsBash: a normal (non-ReadOnly) dispatch keeps Bash so
// the gate can run its check + enact.
func TestDispatchDefaultGrantsBash(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh shim not portable to windows")
	}
	dir := t.TempDir()
	argvFile := filepath.Join(dir, "argv.txt")
	shim := writeArgvShim(t, dir, argvFile)

	disp := ClaudeCLIGateDispatcher{BinaryPath: shim}
	if _, err := disp.Dispatch(context.Background(), GateInput{
		SkillName: "satellites-task-upsert-review", StoryID: "tsk_x", StoryStatus: "ready",
	}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if tools := allowedToolsArg(t, argvFile); tools != gateAllowedTools {
		t.Fatalf("default --allowedTools = %q, want %q", tools, gateAllowedTools)
	}
}
