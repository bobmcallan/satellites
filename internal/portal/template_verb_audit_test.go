// Regression test for story_7b77ffb0 AC12 — `pages/templates/` must
// not reference any retired MCP verb prefixes (`satellites_story_*`,
// `satellites_internal_*`). The verb-namespace flatten landed in
// story_775a7b49; templates were swept clean at that time and the
// portal UI epic guards the invariant going forward.
package portal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTemplates_NoLegacyVerbPrefixes(t *testing.T) {
	t.Parallel()
	root := "../../pages/templates"
	bannedPrefixes := []string{"satellites_story_", "satellites_internal_"}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read %s: %v", root, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(root, e.Name())
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(body)
		for _, banned := range bannedPrefixes {
			if strings.Contains(text, banned) {
				t.Errorf("template %s references retired verb prefix %q (story_775a7b49)", path, banned)
			}
		}
	}
}
