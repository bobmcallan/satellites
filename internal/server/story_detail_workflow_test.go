package server

import (
	"testing"

	"github.com/bobmcallan/satellites/internal/workflow"
)

// TestPickWorkflow pins the sty_20d71a66 governing-workflow selection: an
// exact applies_to category match wins over a "*" wildcard; the wildcard
// governs any category no exact entry claims; nothing applicable → nil.
func TestPickWorkflow(t *testing.T) {
	parent := &workflow.Workflow{Name: "parent-workflow", AppliesTo: []string{"parent"}}
	all := &workflow.Workflow{Name: "satellites-workflow", AppliesTo: []string{"*"}}
	cands := []*workflow.Workflow{parent, all}

	if got := pickWorkflow(cands, "portal"); got != all {
		t.Errorf("portal should resolve to the wildcard workflow, got %+v", got)
	}
	if got := pickWorkflow(cands, "parent"); got != parent {
		t.Errorf("parent must win by exact match over the wildcard, got %+v", got)
	}
	if got := pickWorkflow([]*workflow.Workflow{parent}, "cli"); got != nil {
		t.Errorf("no exact match and no wildcard should be nil, got %+v", got)
	}
}
