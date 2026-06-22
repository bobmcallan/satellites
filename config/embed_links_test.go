package substrate

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

var embedWikilinkRe = regexp.MustCompile(`\[\[([a-z0-9_-]+)\]\]`)

// TestAuthoringReferenceShips guards sty_0993988b: the workflow/reviewer authoring
// reference must ship in the SYSTEM embed so a CONSUMER repo can fetch it via
// document_get / document index. The format reference was previously only in
// satellites' own project substrate, so a consumer-repo agent had nothing to copy
// and fumbled the type:skill + kind:<...> convention by trial and error.
func TestAuthoringReferenceShips(t *testing.T) {
	if _, err := FS.ReadFile("documents/workflow-authoring.md"); err != nil {
		t.Fatalf("workflow-authoring reference missing from the embed — a consumer repo would have no authoring format reference: %v", err)
	}
}

// TestEmbeddedWikilinksResolve guards against a dangling [[wikilink]] in any
// embedded substrate artifact (sty_4bd0a0e9). The satellites-task-workflow linked
// [[reviewer-only-model]], which lived ONLY in the satellites repo's .satellites/
// project substrate — so for every CONSUMER repo the link resolved from no tier
// and the workflow-review link-walker could block task governance; the satellites
// repo masked the defect locally. This walks the SHIPPED embed and fails when a
// [[target]] names no embedded principles/documents/skills artifact — catching the
// next dangling system reference in CI, not in a consumer repo.
func TestEmbeddedWikilinksResolve(t *testing.T) {
	resolvable := map[string]bool{}
	for _, dir := range []string{"principles", "documents", "skills"} {
		entries, err := fs.ReadDir(FS, dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if n := strings.TrimSuffix(e.Name(), ".md"); n != e.Name() {
				resolvable[n] = true
			}
		}
	}
	// "wikilink" is the literal placeholder prose uses to document the [[wikilink]]
	// convention itself — never a real reference.
	resolvable["wikilink"] = true

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
		for _, m := range embedWikilinkRe.FindAllStringSubmatch(string(raw), -1) {
			if target := m[1]; !resolvable[target] {
				t.Errorf("%s: dangling [[%s]] — no embedded principles/documents/skills artifact ships it; a consumer repo would resolve it from no tier", path, target)
			}
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
