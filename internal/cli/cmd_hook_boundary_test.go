package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestGateOutcome_BoundaryAndUngated pins sty_11a6077c: the door governs the
// repo tree, not Claude's self-maintenance. An outside-repo target is allowed
// even with no engagement; an in-repo target is still gated; an ungated_dirs
// entry exempts an in-repo path.
func TestGateOutcome_BoundaryAndUngated(t *testing.T) {
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)

	// A configured repo with NO engagement (the post-epic situation).
	repo := writeRepo(t, true, "")

	// 1. Outside-repo target (e.g. ~/.claude / agent memory) → allow, no engagement.
	outside := filepath.Join(t.TempDir(), "claude-home", "memory", "note.md")
	if allow, _ := gateOutcome(repo, "sess1", outside, now); !allow {
		t.Errorf("outside-repo target must be ungated even with no engagement")
	}

	// 2. In-repo target (the repo's own .claude/) → deny (still gated).
	inRepo := filepath.Join(repo, ".claude", "settings.json")
	if allow, reason := gateOutcome(repo, "sess1", inRepo, now); allow || !strings.Contains(reason, "work init") {
		t.Errorf("in-repo target with no engagement must be gated: allow=%v reason=%q", allow, reason)
	}

	// 3. In-repo target listed in ungated_dirs → allow even with no engagement.
	repo2 := t.TempDir()
	sat := filepath.Join(repo2, ".satellites")
	if err := os.MkdirAll(sat, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sat, "satellites.toml"),
		[]byte("server_url=\"x\"\nungated_dirs = [\"docs/scratch\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scratch := filepath.Join(repo2, "docs", "scratch", "draft.md")
	if allow, _ := gateOutcome(repo2, "sess1", scratch, now); !allow {
		t.Errorf("in-repo target under ungated_dirs must be allowed")
	}
	// A sibling in-repo path NOT under ungated_dirs is still gated.
	other := filepath.Join(repo2, "internal", "x.go")
	if allow, _ := gateOutcome(repo2, "sess1", other, now); allow {
		t.Errorf("in-repo target outside ungated_dirs must stay gated")
	}

	// 4. No target → falls through to the engagement check (deny here).
	if allow, _ := gateOutcome(repo, "sess1", "", now); allow {
		t.Errorf("empty target with no engagement must fall through to the gate (deny)")
	}
}

// TestPathIsUngated pins the pure boundary + glob/~-expansion logic.
func TestPathIsUngated(t *testing.T) {
	root := filepath.Clean(t.TempDir())
	home, _ := os.UserHomeDir()

	// Outside the repo → ungated regardless of the list.
	if !pathIsUngated(filepath.Join(filepath.Dir(root), "elsewhere.md"), root, nil) {
		t.Errorf("a path outside the repo must be ungated")
	}
	// ~/.claude resolves outside the repo (a temp root) → ungated.
	if home != "" && !pathIsUngated(filepath.Join(home, ".claude", "x.md"), root, nil) {
		t.Errorf("~/.claude must be ungated (outside the repo)")
	}
	// Inside the repo, not listed → gated.
	if pathIsUngated(filepath.Join(root, "internal", "x.go"), root, nil) {
		t.Errorf("an in-repo path with no exemption must be gated")
	}
	// Inside the repo, under a relative ungated_dirs entry → ungated.
	if !pathIsUngated(filepath.Join(root, "docs", "scratch", "n.md"), root, []string{"docs/scratch"}) {
		t.Errorf("a relative ungated_dirs entry must exempt the in-repo path")
	}
	// ~ expansion in an ungated_dirs entry.
	if home != "" {
		target := filepath.Join(home, ".cache", "sat", "f")
		if !pathIsUngated(target, root, []string{"~/.cache/sat"}) {
			t.Errorf("a ~-prefixed ungated_dirs entry must expand to $HOME")
		}
	}
	// Glob match.
	if !pathIsUngated(filepath.Join(root, "gen", "a.pb.go"), root, []string{filepath.Join(root, "gen", "*.pb.go")}) {
		t.Errorf("a glob ungated_dirs entry must match")
	}
}

// TestEnsureUngatedDirsBlock pins AC3: the block is appended to an existing toml
// exactly once (idempotent), and an existing key is left untouched.
func TestEnsureUngatedDirsBlock(t *testing.T) {
	dir := t.TempDir()
	tomlPath := filepath.Join(dir, "satellites.toml")
	if err := os.WriteFile(tomlPath, []byte("server_url = \"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// First run appends.
	appended, err := ensureUngatedDirsBlock(tomlPath)
	if err != nil || !appended {
		t.Fatalf("first run: appended=%v err=%v, want appended", appended, err)
	}
	body, _ := os.ReadFile(tomlPath)
	if c := strings.Count(string(body), ungatedDirsKey); c == 0 {
		t.Fatalf("ungated_dirs note not present after append")
	}
	first := string(body)

	// Second run is a no-op (idempotent).
	appended2, err := ensureUngatedDirsBlock(tomlPath)
	if err != nil || appended2 {
		t.Fatalf("second run: appended=%v err=%v, want no-op", appended2, err)
	}
	body2, _ := os.ReadFile(tomlPath)
	if string(body2) != first {
		t.Fatalf("second run changed the file (not idempotent)")
	}

	// A toml with an ACTIVE key is also left untouched.
	active := filepath.Join(t.TempDir(), "satellites.toml")
	if err := os.WriteFile(active, []byte("ungated_dirs = [\"~/.claude\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if appended, _ := ensureUngatedDirsBlock(active); appended {
		t.Fatalf("a toml already carrying ungated_dirs must not be appended to")
	}
}
