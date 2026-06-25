package processtrace

import (
	"strings"
	"testing"
)

// TestActualWorkflow_YAMLAndMermaid drives a story to done through a reject loop
// and checks the actual-workflow projections: the YAML carries each edge's
// status and reject count and the close-out, and the mermaid renders a node per
// state, the current marker, and the reject loop as a dotted edge.
func TestActualWorkflow_YAMLAndMermaid(t *testing.T) {
	entries := []LedgerEntry{
		ent("review_accept", "plan ready", map[string]any{"gate": "satellites-story-plan-review", "from_status": "backlog", "to_status": "in_progress"}, 0),
		ent("status_transition", "backlog → in_progress", map[string]any{"from_status": "backlog", "to_status": "in_progress"}, 1),
		ent("review_reject", "AC1 unmet", map[string]any{"gate": "satellites-story-done-review", "from_status": "in_progress"}, 2),
		ent("review_accept", "all ACs met", map[string]any{"gate": "satellites-story-done-review", "from_status": "in_progress", "to_status": "done"}, 3),
		ent("status_transition", "in_progress → done", map[string]any{"from_status": "in_progress", "to_status": "done"}, 4),
	}
	tags := []string{"estimate-minutes:30", "estimate-tokens:40000", "actual-tokens:38000"}
	pt := Reconcile("sty_doc", "fix", "done", fixWorkflow(), entries, tags)

	doc := ActualWorkflow(pt)
	if doc.Workflow != "satellites-fix-workflow" || doc.CurrentStatus != "done" || !doc.Terminal {
		t.Fatalf("doc head = %+v", doc)
	}
	done := findActual(doc, "in_progress", "done")
	if done == nil || done.Status != StatusAccepted || done.RejectCount != 1 {
		t.Fatalf("done edge = %+v", done)
	}
	if doc.CloseOut.EstimateMinutes != 30 || doc.CloseOut.EstimateTokens != 40000 || doc.CloseOut.TokensActual != 38000 {
		t.Fatalf("close-out = %+v", doc.CloseOut)
	}
	if doc.CloseOut.TotalRejects != 1 {
		t.Fatalf("total rejects = %d want 1", doc.CloseOut.TotalRejects)
	}

	y, err := doc.YAML()
	if err != nil {
		t.Fatalf("yaml: %v", err)
	}
	for _, want := range []string{"workflow: satellites-fix-workflow", "current_status: done", "reject_count: 1", "tokens_actual: 38000"} {
		if !strings.Contains(y, want) {
			t.Fatalf("yaml missing %q:\n%s", want, y)
		}
	}

	m := MermaidActualWorkflow(pt)
	if !strings.HasPrefix(m, "flowchart LR") {
		t.Fatalf("mermaid prefix:\n%s", m)
	}
	// ASCII-safe labels (sty_6630023e): no glyphs, LR, "(current)" marker, "xN"
	// reject count; the actual journey (accepted edge) is drawn.
	for _, want := range []string{`done["done (current)"]`, "story-done accepted x1", "backlog"} {
		if !strings.Contains(m, want) {
			t.Fatalf("mermaid missing %q:\n%s", want, m)
		}
	}
	// No glyphs or quoted-label-breaking characters survive.
	for _, banned := range []string{"◀", "✓", "✗", "…", "·", "×"} {
		if strings.Contains(m, banned) {
			t.Fatalf("mermaid must be ASCII-safe, found %q:\n%s", banned, m)
		}
	}
}

func findActual(d ActualWorkflowDoc, from, to string) *ActualTransition {
	for i := range d.Transitions {
		if d.Transitions[i].From == from && d.Transitions[i].To == to {
			return &d.Transitions[i]
		}
	}
	return nil
}
