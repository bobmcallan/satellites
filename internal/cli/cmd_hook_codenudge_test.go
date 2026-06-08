package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// codeNudgeRepo lays down a configured repo: .satellites/satellites.toml (so the
// repo root resolves) and optionally an index.db + a source file of size bytes.
func codeNudgeRepo(t *testing.T, withIndex bool, srcName string, size int) string {
	t.Helper()
	root := t.TempDir()
	sat := filepath.Join(root, ".satellites")
	if err := os.MkdirAll(sat, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sat, "satellites.toml"), []byte("server_url = \"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if withIndex {
		if err := os.WriteFile(filepath.Join(sat, "index.db"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if srcName != "" {
		body := "package p\n" + strings.Repeat("// filler line to grow the file\n", size/30+1)
		if err := os.WriteFile(filepath.Join(root, srcName), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestAssessCodeNudge_FiresOnLargeIndexedSource(t *testing.T) {
	root := codeNudgeRepo(t, true, "big.go", codeNudgeMinBytes*2)
	nudge, msg := assessCodeNudge(root, filepath.Join(root, "big.go"))
	if !nudge {
		t.Fatal("expected a nudge for a large indexed source file")
	}
	if !strings.Contains(msg, "code symbol") || !strings.Contains(msg, "code search") {
		t.Errorf("nudge message should name code symbol/search: %q", msg)
	}
}

func TestAssessCodeNudge_SilentWithoutIndex(t *testing.T) {
	root := codeNudgeRepo(t, false, "big.go", codeNudgeMinBytes*2)
	if nudge, _ := assessCodeNudge(root, filepath.Join(root, "big.go")); nudge {
		t.Error("no index.db → must stay silent (code search has nothing to offer)")
	}
}

func TestAssessCodeNudge_SilentForSmallFile(t *testing.T) {
	root := codeNudgeRepo(t, true, "small.go", 100)
	if nudge, _ := assessCodeNudge(root, filepath.Join(root, "small.go")); nudge {
		t.Error("small file → must stay silent")
	}
}

func TestAssessCodeNudge_SilentForNonSource(t *testing.T) {
	root := codeNudgeRepo(t, true, "", 0)
	big := filepath.Join(root, "data.bin")
	if err := os.WriteFile(big, bytes.Repeat([]byte("x"), codeNudgeMinBytes*2), 0o644); err != nil {
		t.Fatal(err)
	}
	if nudge, _ := assessCodeNudge(root, big); nudge {
		t.Error("non-source extension → must stay silent")
	}
}

func TestAssessCodeNudge_SilentOutsideRepo(t *testing.T) {
	root := codeNudgeRepo(t, true, "", 0)
	outside := filepath.Join(t.TempDir(), "big.go")
	if err := os.WriteFile(outside, bytes.Repeat([]byte("a"), codeNudgeMinBytes*2), 0o644); err != nil {
		t.Fatal(err)
	}
	if nudge, _ := assessCodeNudge(root, outside); nudge {
		t.Error("target outside the repo → must stay silent")
	}
}

func TestRunHookCodeNudge_EmptyEventSilent(t *testing.T) {
	var out bytes.Buffer
	if err := runHookCodeNudge(strings.NewReader(""), &out); err != nil {
		t.Fatalf("empty event should be tolerated: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("empty event must emit nothing, got: %q", out.String())
	}
}

func TestRunHookCodeNudge_EmitsAdditionalContext(t *testing.T) {
	root := codeNudgeRepo(t, true, "big.go", codeNudgeMinBytes*2)
	ev := `{"cwd":"` + root + `","tool_name":"Read","tool_input":{"file_path":"` +
		filepath.Join(root, "big.go") + `"}}`
	var out bytes.Buffer
	if err := runHookCodeNudge(strings.NewReader(ev), &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "additionalContext") || !strings.Contains(got, "code symbol") {
		t.Errorf("expected advisory additionalContext, got: %q", got)
	}
}
