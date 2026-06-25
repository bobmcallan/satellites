package processtrace

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// This file renders a reconciled ProcessTrace as the ACTUAL-workflow projections
// the story-change document carries (sty_201dc6c6): a YAML record of the
// traversal and a mermaid flowchart of it. Both are pure functions over the
// already-reconciled trace — no fresh ledger pass — so the change-document
// generator stays a thin consumer of Reconcile's output.

// ActualWorkflowDoc is the serializable projection of a story's actual workflow
// traversal — declared edges annotated with what happened to each, plus the
// close-out. It marshals to the YAML block the change document embeds.
type ActualWorkflowDoc struct {
	Workflow      string             `yaml:"workflow"`
	CurrentStatus string             `yaml:"current_status"`
	Terminal      bool               `yaml:"terminal"`
	Transitions   []ActualTransition `yaml:"transitions"`
	CloseOut      ActualCloseOut     `yaml:"close_out"`
}

// ActualTransition is one declared edge annotated with its actual outcome.
type ActualTransition struct {
	From        string `yaml:"from"`
	To          string `yaml:"to"`
	Gate        string `yaml:"gate,omitempty"`
	Status      string `yaml:"status"`
	RejectCount int    `yaml:"reject_count,omitempty"`
	At          string `yaml:"at,omitempty"`
}

// ActualCloseOut is the estimate-vs-actual tail of the YAML projection.
type ActualCloseOut struct {
	EstimateMinutes int `yaml:"estimate_minutes,omitempty"`
	EstimateTokens  int `yaml:"estimate_tokens,omitempty"`
	ElapsedMinutes  int `yaml:"elapsed_minutes,omitempty"`
	TokensActual    int `yaml:"tokens_actual,omitempty"`
	TotalRejects    int `yaml:"total_rejects"`
}

// ActualWorkflow projects a reconciled ProcessTrace into the serializable
// actual-workflow record.
func ActualWorkflow(pt ProcessTrace) ActualWorkflowDoc {
	doc := ActualWorkflowDoc{
		Workflow:      pt.WorkflowName,
		CurrentStatus: pt.CurrentStatus,
		Terminal:      pt.CloseOut.Terminal,
	}
	for _, tr := range pt.Transitions {
		at := ""
		if tr.At != nil {
			at = tr.At.UTC().Format("2006-01-02 15:04")
		}
		doc.Transitions = append(doc.Transitions, ActualTransition{
			From:        tr.From,
			To:          tr.To,
			Gate:        tr.ReviewerSkill,
			Status:      tr.Status,
			RejectCount: tr.RejectCount,
			At:          at,
		})
	}
	doc.CloseOut = ActualCloseOut{TotalRejects: pt.CloseOut.TotalRejects, ElapsedMinutes: pt.CloseOut.ElapsedMinutes}
	if pt.CloseOut.Estimate != nil {
		doc.CloseOut.EstimateMinutes = pt.CloseOut.Estimate.TimeMinutes
		doc.CloseOut.EstimateTokens = pt.CloseOut.Estimate.Tokens
	}
	if pt.CloseOut.TokensActual != nil {
		doc.CloseOut.TokensActual = *pt.CloseOut.TokensActual
	}
	return doc
}

// YAML marshals the actual-workflow projection to a YAML string.
func (d ActualWorkflowDoc) YAML() (string, error) {
	b, err := yaml.Marshal(d)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// statusWord maps a transition's actual status to a compact ASCII edge label.
// ASCII-only and glyph-free so mermaid 11.x parses it reliably (sty_6630023e):
// the earlier glyph labels (✓ → ✗ … ·) tripped "Syntax error in text".
func statusWord(status string) string {
	switch status {
	case StatusAccepted:
		return "accepted"
	case StatusFired:
		return "fired"
	case StatusRejected:
		return "rejected"
	case StatusPending:
		return "pending"
	default:
		return "not reached"
	}
}

// mermaidNodeID sanitizes a state name into a mermaid-safe node id.
func mermaidNodeID(state string) string {
	return strings.NewReplacer("-", "_", " ", "_", ":", "_").Replace(state)
}

// mermaidLabel makes a string safe inside a quoted mermaid node/edge label —
// strips the double-quote and pipe that would break the `["..."]` / `|"..."|`
// delimiters, and drops non-ASCII glyphs that the parser rejects.
func mermaidLabel(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '"' || r == '|':
			b.WriteByte(' ')
		case r > 126: // drop non-ASCII (glyphs like ◀ ✓ … ·)
			continue
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// MermaidActualWorkflow renders the traversal as a left-to-right mermaid
// flowchart of the ACTUAL JOURNEY (sty_6630023e). Only edges that were reached —
// accepted, fired, rejected (the loops), and the single current pending edge —
// are drawn; "not reached" edges (cancel paths, unentered fail loops) are
// dropped, so the diagram reads as a clean pipeline and the dense-graph dagre
// layout error ("could not find a suitable point") does not occur. A reject loop
// reads as a dotted arrow; the current state is marked; reject counts show when
// non-zero so a challenged-and-reprocessed story visibly loops.
func MermaidActualWorkflow(pt ProcessTrace) string {
	var b strings.Builder
	b.WriteString("flowchart LR\n")

	// Keep only the edges that are part of the actual journey, in declared order.
	var edges []TransitionTrace
	for _, tr := range pt.Transitions {
		if tr.Status == StatusNotReached {
			continue
		}
		edges = append(edges, tr)
	}

	// Collect the states on the kept edges (plus the current state), in stable
	// first-appearance order, so the node set matches the rendered edges.
	seen := map[string]bool{}
	var states []string
	addState := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			states = append(states, s)
		}
	}
	for _, tr := range edges {
		addState(tr.From)
		addState(tr.To)
	}
	addState(pt.CurrentStatus)

	for _, s := range states {
		label := s
		if s == pt.CurrentStatus {
			label = s + " (current)"
		}
		fmt.Fprintf(&b, "  %s[\"%s\"]\n", mermaidNodeID(s), mermaidLabel(label))
	}

	for _, tr := range edges {
		lbl := statusWord(tr.Status)
		if tr.ReviewerSkill != "" {
			lbl = shortGate(tr.ReviewerSkill) + " " + lbl
		}
		if tr.RejectCount > 0 {
			lbl = fmt.Sprintf("%s x%d", lbl, tr.RejectCount)
		}
		arrow := "-->"
		if tr.Status == StatusRejected || tr.Status == StatusPending {
			arrow = "-.->" // a failed or not-yet-fired edge reads dotted
		}
		fmt.Fprintf(&b, "  %s %s|\"%s\"| %s\n", mermaidNodeID(tr.From), arrow, mermaidLabel(lbl), mermaidNodeID(tr.To))
	}
	return b.String()
}

// shortGate trims the satellites- prefix and -review suffix from a gate skill
// name for a compact edge label.
func shortGate(skill string) string {
	s := strings.TrimPrefix(skill, "satellites-")
	s = strings.TrimSuffix(s, "-review")
	return s
}
