package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestArchiveWorkflows: no-op on an absent/empty dir; a populated dir is moved
// reversibly to the next free workflows.archive-N.
func TestArchiveWorkflows(t *testing.T) {
	repo := t.TempDir()
	if _, archived, err := archiveWorkflows(repo); err != nil || archived {
		t.Fatalf("absent dir: archived=%v err=%v, want no-op", archived, err)
	}

	wfDir := filepath.Join(repo, ".satellites", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wfDir, "w.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest, archived, err := archiveWorkflows(repo)
	if err != nil || !archived {
		t.Fatalf("populated: archived=%v err=%v", archived, err)
	}
	if filepath.Base(dest) != "workflows.archive-1" {
		t.Errorf("dest=%s, want .../workflows.archive-1", dest)
	}
	if _, e := os.Stat(wfDir); !os.IsNotExist(e) {
		t.Errorf("original workflows dir should be gone after archive")
	}

	// A second archive lands in archive-2 (the previous one is preserved).
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wfDir, "w.md"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest2, _, _ := archiveWorkflows(repo)
	if filepath.Base(dest2) != "workflows.archive-2" {
		t.Errorf("dest2=%s, want .../workflows.archive-2", dest2)
	}
}

// TestRunRebase_HooksReconcile: rebase --hooks adds the hooks the current init
// defines but a stale settings.json is missing (the goal-keeper Stop hook +
// always-context), non-destructively.
func TestRunRebase_HooksReconcile(t *testing.T) {
	repo := t.TempDir()
	claude := filepath.Join(repo, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	// A stale settings.json with no hooks.
	if err := os.WriteFile(filepath.Join(claude, "settings.json"), []byte(`{"hooks":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runRebase(&out, repo, false, true); err != nil {
		t.Fatalf("rebase --hooks: %v", err)
	}
	s, _ := os.ReadFile(filepath.Join(claude, "settings.json"))
	if !commandUnderEvent(t, s, "Stop", "", stopCheckCommand) {
		t.Errorf("rebase --hooks did not add the Stop goal-keeper hook:\n%s", s)
	}
	if !commandUnderEvent(t, s, "SessionStart", "", sessionContextCommand) {
		t.Errorf("rebase --hooks did not add the always-context hook")
	}
}

// TestRunRebase_WarnsUpFront (sty_381050ee): rebase prints the up-front,
// reversible-action warning BEFORE it acts, scoped to the active flags, and
// stays non-interactive (no prompt — runRebase takes no input).
func TestRunRebase_WarnsUpFront(t *testing.T) {
	repo := t.TempDir()
	var out bytes.Buffer
	if err := runRebase(&out, repo, true, true); err != nil {
		t.Fatalf("rebase: %v", err)
	}
	s := out.String()
	for _, want := range []string{
		"rebase will modify",
		"reversible",
		"archive .satellites/workflows/",
		".claude/settings.json hooks",
		"satellites skill sync",
		"--workflows / --hooks",
	} {
		if !bytes.Contains([]byte(s), []byte(want)) {
			t.Errorf("warning missing %q:\n%s", want, s)
		}
	}
	// Scope narrowing: a hooks-only rebase must not warn about archiving workflows.
	var hooksOnly bytes.Buffer
	if err := runRebase(&hooksOnly, t.TempDir(), false, true); err != nil {
		t.Fatalf("rebase --hooks: %v", err)
	}
	if bytes.Contains(hooksOnly.Bytes(), []byte("archive .satellites/workflows/")) {
		t.Errorf("hooks-only rebase must not warn about archiving workflows:\n%s", hooksOnly.String())
	}
}

// TestRunRebase_WorkflowsBaseline: rebase --workflows archives the current
// workflows and scaffolds the gateless baseline.
func TestRunRebase_WorkflowsBaseline(t *testing.T) {
	repo := t.TempDir()
	wfDir := filepath.Join(repo, ".satellites", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wfDir, "old.md"), []byte("old workflow"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runRebase(&out, repo, true, false); err != nil {
		t.Fatalf("rebase --workflows: %v", err)
	}
	// rebase --workflows archives the repo's workflows so the EMBEDDED baseline +
	// parent defaults govern — it no longer scaffolds a copy (sty_a69e8c61).
	if _, err := os.Stat(filepath.Join(repo, ".satellites", "workflows.archive-1", "old.md")); err != nil {
		t.Errorf("old workflow not archived")
	}
	if _, err := os.Stat(filepath.Join(wfDir, "satellites-baseline-workflow.md")); !os.IsNotExist(err) {
		t.Errorf("rebase must NOT scaffold a baseline copy (the embed governs)")
	}
}
