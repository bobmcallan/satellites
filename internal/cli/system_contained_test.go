package cli

import (
	"io/fs"
	"strings"
	"testing"

	substrate "github.com/bobmcallan/satellites/config"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workflow"
)

// TestConfigWorkflowsSystemContained pins the system-contained invariant
// (epic:system-substrate, story 5) at build time: every workflow embedded under
// config/workflows/ is SYSTEM substrate shipped in the client binary, so every
// reviewer_skill it names MUST resolve from the binary embed (config/skills,
// verb.IsConfigSkill) — not the server, not a repo's .claude/skills. A default
// process that needs a server or a local file to run its own gates is not
// self-contained. This is the build-time belt to the runtime checkSystemContained
// braces.
func TestConfigWorkflowsSystemContained(t *testing.T) {
	entries, err := fs.ReadDir(substrate.FS, "workflows")
	if err != nil {
		t.Fatalf("read config/workflows embed: %v", err)
	}
	seen := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		raw, err := fs.ReadFile(substrate.FS, "workflows/"+e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		wf, err := workflow.Parse(raw)
		if err != nil || wf == nil {
			t.Fatalf("%s: does not parse as a workflow: %v", e.Name(), err)
		}
		seen++
		for _, tr := range wf.Transitions {
			g := strings.TrimSpace(tr.ReviewerSkill)
			if g == "" {
				continue
			}
			if !verb.IsConfigSkill(g) {
				t.Errorf("%s names reviewer %q which is NOT resolvable from the config/skills binary embed — a system workflow must be self-contained (move the reviewer into config/skills)", e.Name(), g)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no config/workflows/*.md found — the system-contained invariant has no subject")
	}
}
