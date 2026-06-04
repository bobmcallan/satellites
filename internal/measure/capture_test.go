package measure

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAppendEventWritesInRepo is the M1 acceptance check: a captured event
// lands as a JSONL line under the in-repo log dir — not in $HOME, and not
// under the separate daemon's ~/.satellites/daemon path.
func TestAppendEventWritesInRepo(t *testing.T) {
	repo := t.TempDir() // stand-in for the consumer repo root
	logDir := filepath.Join(repo, ".satellites", "logs")

	if err := AppendEvent(logDir, "sess-1", map[string]any{"type": "tool_call", "seq": 0}); err != nil {
		t.Fatal(err)
	}
	if err := AppendEvent(logDir, "sess-1", map[string]any{"type": "tool_result", "seq": 1}); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(logDir, "sess-1.jsonl")
	if !strings.HasPrefix(path, repo) {
		t.Fatalf("log path %q escaped the repo root %q", path, repo)
	}
	if home, _ := os.UserHomeDir(); home != "" && strings.HasPrefix(path, filepath.Join(home, ".satellites")) {
		t.Fatalf("log path %q landed under $HOME/.satellites (should be in-repo)", path)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer f.Close()
	lines := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("line %d is not valid JSON: %v", lines, err)
		}
		lines++
	}
	if lines != 2 {
		t.Fatalf("want 2 appended JSONL lines, got %d", lines)
	}
}

// TestAppendEventFlattensSessionName ensures a malicious/path-bearing session
// id cannot escape the log dir.
func TestAppendEventFlattensSessionName(t *testing.T) {
	logDir := t.TempDir()
	if err := AppendEvent(logDir, "../escape", map[string]any{"x": 1}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("want exactly 1 file in logDir, got %d", len(entries))
	}
	name := entries[0].Name()
	if strings.Contains(name, "/") || strings.Contains(name, "..") {
		t.Fatalf("session name not flattened: %q", name)
	}
}

func TestAppendEventEmptyLogDir(t *testing.T) {
	if err := AppendEvent("", "s", map[string]any{}); err == nil {
		t.Fatal("expected error for empty logDir")
	}
}
