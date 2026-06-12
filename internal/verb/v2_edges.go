// Package-internal v2 edge planner (epic:graduated-workflow, story
// dispatcher-loop-enforcement). A v2 workflow expresses a review as a STATE
// whose outgoing edges carry `on: pass|fail`; on such a state the gate skill
// JUDGES ONLY and the CLIENT enacts — the reviewer's decision selects the
// edge, fail loops are bounded by max_iterations counted from
// reviewer-written ledger rows, and exhaustion lands the story in the
// declared escalation state by a client-written transition. Everything here
// is pure (no I/O) so the dispatcher's behaviour is fixture-testable; the
// CLI calls these instead of importing internal/workflow (its layering
// guard).

package verb

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bobmcallan/satellites/internal/workflow"
)

// V2Edges is the dispatch-relevant projection of one state's v2 edge set,
// resolved from a story's embedded `## Workflow`.
type V2Edges struct {
	// IsV2 is true when the current status has `on: pass|fail` outgoing
	// edges — the client-enactment path. False = legacy (skill enacts).
	IsV2 bool
	// Actor / Command come from the state declaration (open vocabulary;
	// executor/reviewer/satellites/operator are the reserved names).
	Actor   string
	Command string
	// PassTo / FailTo are the targets the decision selects.
	PassTo string
	FailTo string
	// MaxIterations / OnExhausted bound the fail loop (0/"" = unbounded
	// fail edge, which validation only permits outside cycles).
	MaxIterations int
	OnExhausted   string
}

// ResolveV2Edges parses the story body's embedded workflow and projects the
// current status's v2 edge set. A body with no parseable workflow, an
// undeclared status, or no `on` edges from the status resolves to the legacy
// path (IsV2 false, nil error) — the caller falls through unchanged.
func ResolveV2Edges(storyBody, status string) (V2Edges, error) {
	wf, err := workflow.ParseBody([]byte(storyBody))
	if err != nil {
		return V2Edges{}, nil // no/invalid workflow → legacy path, plan-review owns the shape
	}
	st, ok := wf.StateOf(strings.TrimSpace(status))
	if !ok {
		return V2Edges{}, nil
	}
	out := V2Edges{Actor: st.Actor, Command: st.Command}
	for _, t := range wf.TransitionsFrom(st.Name) {
		switch t.On {
		case "pass":
			out.IsV2 = true
			out.PassTo = t.To
		case "fail":
			out.IsV2 = true
			out.FailTo = t.To
			out.MaxIterations = t.MaxIterations
			out.OnExhausted = t.OnExhausted
		}
	}
	return out, nil
}

// CheckpointEdge resolves the single ungated `trigger: checkpoint` edge out
// of the current status — the edge the executor's checkpoint enacts
// deterministically (no gate, no judgment). ok is false when the state has
// no such edge, more than one, or any `on` edges (a review state is never a
// checkpoint source).
func CheckpointEdge(storyBody, status string) (string, bool) {
	wf, err := workflow.ParseBody([]byte(storyBody))
	if err != nil {
		return "", false
	}
	to, count := "", 0
	for _, t := range wf.TransitionsFrom(strings.TrimSpace(status)) {
		if t.On != "" {
			return "", false
		}
		if t.Trigger == "checkpoint" && t.ReviewerSkill == "" {
			to, count = t.To, count+1
		}
	}
	return to, count == 1
}

// PlannedRow is one ledger row the dispatcher must append, in order.
type PlannedRow struct {
	Kind    string
	Body    string
	Payload map[string]any
}

// V2Plan is the deterministic enactment for one gate decision.
type V2Plan struct {
	// ToStatus is the status the client enacts ("" = none, refusal case).
	ToStatus string
	// Exhausted marks a plan that landed in the escalation state.
	Exhausted bool
	// Rows are the ledger rows to append, in order.
	Rows []PlannedRow
}

// PlanV2Enactment maps a reviewer decision onto the v2 edge set. priorRejects
// is the count of this edge's review_reject rows already on the ledger
// (CountEdgeRejects); the reject that makes the count reach MaxIterations
// enacts the exhaustion transition instead of the fail edge.
func PlanV2Enactment(decision, gate, fromStatus, notes string, e V2Edges, priorRejects int) (V2Plan, error) {
	if !e.IsV2 {
		return V2Plan{}, fmt.Errorf("v2 plan: state %q has no on:pass|fail edges", fromStatus)
	}
	switch decision {
	case GateDecisionAccept:
		if e.PassTo == "" {
			return V2Plan{}, fmt.Errorf("v2 plan: state %q has no on:pass edge to enact an accept", fromStatus)
		}
		return V2Plan{
			ToStatus: e.PassTo,
			Rows: []PlannedRow{
				{Kind: "review_accept", Body: notes,
					Payload: map[string]any{"from_status": fromStatus, "to_status": e.PassTo, "gate": gate, "on": "pass"}},
				{Kind: "status_transition", Body: fmt.Sprintf("%s → %s", fromStatus, e.PassTo),
					Payload: map[string]any{"from_status": fromStatus, "to_status": e.PassTo, "on": "pass", "gate": gate}},
			},
		}, nil
	case GateDecisionReject:
		if e.FailTo == "" {
			return V2Plan{}, fmt.Errorf("v2 plan: state %q has no on:fail edge to enact a reject", fromStatus)
		}
		rejectRow := PlannedRow{Kind: "review_reject", Body: notes,
			Payload: map[string]any{"from_status": fromStatus, "gate": gate, "on": "fail"}}
		if e.MaxIterations > 0 && priorRejects+1 >= e.MaxIterations {
			if e.OnExhausted == "" {
				return V2Plan{}, fmt.Errorf("v2 plan: state %q fail edge is bounded but names no on_exhausted", fromStatus)
			}
			return V2Plan{
				ToStatus:  e.OnExhausted,
				Exhausted: true,
				Rows: []PlannedRow{
					rejectRow,
					{Kind: "status_transition",
						Body: fmt.Sprintf("exhausted: %d/%d rejects on %s — escalating to %s", priorRejects+1, e.MaxIterations, fromStatus, e.OnExhausted),
						Payload: map[string]any{"from_status": fromStatus, "to_status": e.OnExhausted, "exhausted": true,
							"rejects": priorRejects + 1, "max_iterations": e.MaxIterations, "gate": gate}},
				},
			}, nil
		}
		return V2Plan{
			ToStatus: e.FailTo,
			Rows: []PlannedRow{
				rejectRow,
				{Kind: "status_transition", Body: fmt.Sprintf("%s → %s (fail edge)", fromStatus, e.FailTo),
					Payload: map[string]any{"from_status": fromStatus, "to_status": e.FailTo, "on": "fail", "gate": gate}},
			},
		}, nil
	default:
		return V2Plan{}, fmt.Errorf("v2 plan: unmapped gate decision %q", decision)
	}
}

// CountEdgeRejects counts the review_reject rows attributed to the named
// state, scanning oldest-first and re-arming at every exhaustion enactment —
// rejects before the most recent exhaustion belong to a spent loop and must
// not lock a re-armed story out of its bound (operator intervention starts a
// fresh window).
func CountEdgeRejects(entries []LedgerEntryView, status string) int {
	count := 0
	for _, e := range entries {
		switch e.Kind {
		case "review_reject":
			if e.PayloadField("from_status") == status {
				count++
			}
		case "status_transition":
			if e.PayloadBool("exhausted") {
				count = 0
			}
		}
	}
	return count
}

// LedgerEntryView is the minimal row shape the counter reads — kind plus a
// lazily-decoded payload. Constructed from verb ledger entries (or any JSON
// payload-carrying row) by the caller.
type LedgerEntryView struct {
	Kind    string
	Payload json.RawMessage

	decoded map[string]any
}

// PayloadField returns the named payload field as a string ("" when absent
// or not a string).
func (v *LedgerEntryView) PayloadField(name string) string {
	v.decode()
	s, _ := v.decoded[name].(string)
	return s
}

// PayloadBool returns the named payload field as a bool (false when absent).
func (v *LedgerEntryView) PayloadBool(name string) bool {
	v.decode()
	b, _ := v.decoded[name].(bool)
	return b
}

func (v *LedgerEntryView) decode() {
	if v.decoded != nil {
		return
	}
	v.decoded = map[string]any{}
	if len(v.Payload) > 0 {
		_ = json.Unmarshal(v.Payload, &v.decoded)
	}
}
