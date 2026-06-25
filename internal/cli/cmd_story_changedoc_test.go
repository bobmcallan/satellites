package cli

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/processtrace"
	"github.com/bobmcallan/satellites/internal/workflow"
)

// changedocWorkflow is a minimal two-edge workflow so assembleChangedoc has a
// governing workflow to project the actual-workflow diagram from.
func changedocWorkflow() *workflow.Workflow {
	return &workflow.Workflow{
		Name:   "satellites-workflow",
		States: []workflow.State{{Name: "backlog"}, {Name: "in_progress"}, {Name: "done"}},
		Transitions: []workflow.Transition{
			{From: "backlog", To: "in_progress", ReviewerSkill: "satellites-intent-plan-review"},
			{From: "in_progress", To: "done", ReviewerSkill: "satellites-implementation-summary-review"},
		},
	}
}

// TestAssembleChangedocDropsYAML pins sty_18b3f374: the visible change document's
// "Actual workflow" section renders ONLY the mermaid diagram — the raw ```yaml
// projection is gone (AC1), while the mermaid block (AC2) and the git record /
// narrative sections remain.
func TestAssembleChangedocDropsYAML(t *testing.T) {
	git := gitRecord{
		Commits: []string{"abc1234 feat: did the thing (sty_x)"},
		Files:   []string{"internal/cli/cmd_story_changedoc.go"},
	}
	doc := assembleChangedoc(
		"sty_x", "Drop the yaml block", "why we did it",
		git, []processtrace.LedgerEntry{}, "feature", "done", changedocWorkflow(),
	)

	if strings.Contains(doc, "```yaml") {
		t.Errorf("AC1: change document still contains a ```yaml block:\n%s", doc)
	}
	if !strings.Contains(doc, "```mermaid") {
		t.Errorf("AC2: change document missing the mermaid diagram:\n%s", doc)
	}
	if !strings.Contains(doc, "## Actual workflow") {
		t.Errorf("Actual workflow section missing:\n%s", doc)
	}
	// AC2: git record + narrative still present.
	if !strings.Contains(doc, "## Git record") || !strings.Contains(doc, "abc1234") {
		t.Errorf("git record section changed unexpectedly:\n%s", doc)
	}
	if !strings.Contains(doc, "why we did it") {
		t.Errorf("narrative dropped:\n%s", doc)
	}
}

// TestAssembleChangedocNoWorkflow pins the no-workflow path is unaffected: it
// emits neither a yaml nor a mermaid block, just the omitted-projection note.
func TestAssembleChangedocNoWorkflow(t *testing.T) {
	doc := assembleChangedoc("sty_x", "t", "", gitRecord{}, []processtrace.LedgerEntry{}, "feature", "in_progress", nil)
	if strings.Contains(doc, "```yaml") || strings.Contains(doc, "```mermaid") {
		t.Errorf("no-workflow doc should contain no projection blocks:\n%s", doc)
	}
	if !strings.Contains(doc, "workflow projection omitted") {
		t.Errorf("no-workflow note missing:\n%s", doc)
	}
}
