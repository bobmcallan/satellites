package substrate

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/frontmatter"
)

// TestEmbeddedFrontmatterParses YAML-parses every embedded substrate file's
// frontmatter exactly as the runtime (and the server boot-seed) does — so a
// malformed frontmatter fails HERE, in CI, not at server boot.
//
// The sty_50458f17 incident: an unquoted ": " in a reviewer's `description`
// shipped green because the embed audit only regex-scanned prose and never
// parsed the YAML; the server's strict boot-seed then died on it and downed
// pprod. This test closes that CI gap — it walks the embed FS (kind-agnostic,
// so it covers documents/principles/skills/workflows/mcp alike) and runs the
// real frontmatter parser on each file.
func TestEmbeddedFrontmatterParses(t *testing.T) {
	walked := 0
	err := fs.WalkDir(FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		raw, rErr := FS.ReadFile(path)
		if rErr != nil {
			t.Errorf("%s: read: %v", path, rErr)
			return nil
		}
		if _, _, pErr := frontmatter.Parse(raw); pErr != nil {
			t.Errorf("%s: frontmatter does not parse (would be skipped at boot): %v", path, pErr)
		}
		walked++
		return nil
	})
	if err != nil {
		t.Fatalf("walk embed FS: %v", err)
	}
	if walked == 0 {
		t.Fatal("no embedded .md files walked — embed glob or FS is wrong")
	}
}
