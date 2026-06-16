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

// TestSubstrateRootsConfig pins the order-1b contract (sty_f2aa0ab5): the
// authoring-source root for each kind is CONFIGURED in satellites.toml
// ([substrate_roots]), defaulting to .satellites/<kind> — never hardcoded. It
// drives the real CLI: with documents='.' the upload resolves the TOP-LEVEL
// documents/ folder; with no config it defaults to .satellites/documents.
func TestSubstrateRootsConfig(t *testing.T) {
	run := func(repo string) string {
		prev, _ := os.Getwd()
		if err := os.Chdir(repo); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Chdir(prev) }()
		root := cli.NewRootCmd()
		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetErr(&buf)
		root.SetArgs([]string{"document", "upload", "--dry-run", "--config", filepath.Join(repo, ".satellites", "satellites.toml")})
		_ = root.Execute()
		return buf.String()
	}
	writeDoc := func(repo, dir string) {
		if err := os.MkdirAll(filepath.Join(repo, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, dir, "note.md"), []byte("---\nname: note\n---\n# Note\n\nClean prose.\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeToml := func(repo, body string) {
		if err := os.MkdirAll(filepath.Join(repo, ".satellites"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, ".satellites", "satellites.toml"), []byte("project_id = \"proj_test\"\n"+body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("configured documents='.' resolves the top-level folder", func(t *testing.T) {
		repo := t.TempDir()
		writeToml(repo, "[substrate_roots]\ndocuments = \".\"\n")
		writeDoc(repo, "documents") // top-level
		out := run(repo)
		if !strings.Contains(out, filepath.Join("documents", "note.md")) {
			t.Fatalf("documents='.' should resolve the top-level documents/ folder, got:\n%s", out)
		}
	})

	t.Run("default resolves .satellites/documents", func(t *testing.T) {
		repo := t.TempDir()
		writeToml(repo, "") // no [substrate_roots] → default .satellites
		writeDoc(repo, filepath.Join(".satellites", "documents"))
		out := run(repo)
		if !strings.Contains(out, filepath.Join(".satellites", "documents", "note.md")) {
			t.Fatalf("default should resolve .satellites/documents/, got:\n%s", out)
		}
	})
}
