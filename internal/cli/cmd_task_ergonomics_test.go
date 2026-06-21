package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/verb"
)

// TestTaskGetJSONShape pins `task get -o json` (sty_92adea9d AC1): the
// machine-readable shape carries id/title/status/body under stable keys.
func TestTaskGetJSONShape(t *testing.T) {
	j := taskGetJSON{
		ID: "tsk_a", Title: "scan", Status: "running",
		Priority: "low", Category: "improvement", Tags: []string{"x"}, Body: "hello",
	}
	b, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{`"id":"tsk_a"`, `"title":"scan"`, `"status":"running"`, `"body":"hello"`} {
		if !strings.Contains(s, want) {
			t.Errorf("task json missing %q: %s", want, s)
		}
	}
}

// embeddedWFBody wraps a states/transitions yaml block as a story/task body's
// embedded ## Workflow (no workflow frontmatter — it's a body, not a definition).
func embeddedWFBody(states []string) string {
	var b strings.Builder
	b.WriteString("# Task\n\n## Workflow\n\n```yaml\nstates:\n")
	for _, s := range states {
		b.WriteString("  - " + s + "\n")
	}
	b.WriteString("transitions:\n")
	for i := 0; i+1 < len(states); i++ {
		b.WriteString("  - {from: " + states[i] + ", to: " + states[i+1] + ", reviewer_skill: \"r\"}\n")
	}
	b.WriteString("```\n")
	return b.String()
}

// TestEmbeddedWorkflowDriftDetected pins AC3: a body whose embedded ## Workflow
// diverges from the governing workflow is flagged (the warning `task get`
// surfaces). A body matching the governing shape produces no drift.
func TestEmbeddedWorkflowDriftDetected(t *testing.T) {
	gov := wfBody("gov-wf", []string{"feature"}, []string{"backlog", "in_progress", "done"})
	sources := []verb.WorkflowSource{{Name: "gov-wf", Body: gov}}

	// divergent embed (extra "shipping" state) → drift
	diverge := embeddedWFBody([]string{"backlog", "in_progress", "shipping", "done"})
	if _, _, drift := verb.GoverningReconcile("", diverge, "backlog", "feature", sources); strings.TrimSpace(drift) == "" {
		t.Fatal("a body whose embedded ## Workflow diverges from the governing workflow must produce drift")
	}

	// matching embed → no drift
	match := embeddedWFBody([]string{"backlog", "in_progress", "done"})
	if _, _, drift := verb.GoverningReconcile("", match, "backlog", "feature", sources); strings.TrimSpace(drift) != "" {
		t.Errorf("a body matching the governing workflow should not drift; got %q", drift)
	}
}
