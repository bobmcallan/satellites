//go:build integration

package integration_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/cli"
)

// TestTopLevelAuthoringSources pins the order-1 contract (sty_a75dd8c5): the
// satellites client reads authoring sources from TOP-LEVEL repo folders
// (documents/, principles/, skills/), not .satellites/{...}. It drives the real
// CLI: `document upload --dry-run` from a repo with a top-level documents/ plans
// the upsert, and a stale `.satellites/documents/` surfaces the move nudge.
func TestTopLevelAuthoringSources(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".satellites"), 0o755); err != nil {
		t.Fatal(err)
	}
	tomlPath := filepath.Join(repo, ".satellites", "satellites.toml")
	if err := os.WriteFile(tomlPath, []byte("project_id = \"proj_test\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A document authored at the TOP-LEVEL documents/ folder.
	if err := os.MkdirAll(filepath.Join(repo, "documents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "documents", "note.md"), []byte("---\nname: note\n---\n# Note\n\nClean prose.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	prev, _ := os.Getwd()
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	run := func() string {
		root := cli.NewRootCmd()
		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetErr(&buf)
		root.SetArgs([]string{"document", "upload", "--dry-run"})
		_ = root.Execute()
		return buf.String()
	}

	// Resolves the top-level documents/ folder.
	out := run()
	if !strings.Contains(out, filepath.Join("documents", "note.md")) {
		t.Fatalf("document upload should resolve the top-level documents/ folder, got:\n%s", out)
	}
	if strings.Contains(out, ".satellites/documents") {
		t.Fatalf("upload must not read .satellites/documents, got:\n%s", out)
	}

	// A stale .satellites/documents/ with sources surfaces the move nudge.
	if err := os.MkdirAll(filepath.Join(repo, ".satellites", "documents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".satellites", "documents", "old.md"), []byte("---\nname: old\n---\n# Old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Remove the top-level source so upload hits the empty-result + nudge path.
	if err := os.Remove(filepath.Join(repo, "documents", "note.md")); err != nil {
		t.Fatal(err)
	}
	out = run()
	if !strings.Contains(out, "no longer read") {
		t.Fatalf("a stale .satellites/documents/ should surface the move nudge, got:\n%s", out)
	}
}
