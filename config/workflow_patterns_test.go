package substrate

import (
	"regexp"
	"testing"

	"github.com/bobmcallan/satellites/internal/workflow"
)

// TestWorkflowPatternsSnippetsParse pins the workflow-patterns answer sheet
// to the schema (epic:graduated-workflow dot-export-patterns-doc AC3): every
// fenced yaml snippet in the doc must parse as a valid workflow, so the doc
// cannot drift from what the parser accepts.
func TestWorkflowPatternsSnippetsParse(t *testing.T) {
	raw, err := FS.ReadFile("documents/workflow-patterns.md")
	if err != nil {
		t.Fatalf("read workflow-patterns.md: %v", err)
	}
	blocks := regexp.MustCompile("(?s)```yaml\n(.*?)```").FindAllStringSubmatch(string(raw), -1)
	if len(blocks) != 5 {
		t.Fatalf("workflow-patterns.md carries %d yaml snippets, want 5 (one per pattern)", len(blocks))
	}
	for i, m := range blocks {
		body := []byte("# snippet\n\n```yaml\n" + m[1] + "```\n")
		wf, perr := workflow.ParseBody(body)
		if perr != nil {
			t.Errorf("pattern snippet %d does not parse: %v", i+1, perr)
			continue
		}
		if lerr := wf.ValidateLifecycle(); lerr != nil {
			t.Errorf("pattern snippet %d fails lifecycle validation: %v", i+1, lerr)
		}
	}
}
