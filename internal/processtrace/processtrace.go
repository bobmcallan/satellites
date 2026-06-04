// Package processtrace reconciles a story's DECLARED process (its workflow
// skill: states/transitions/gates) against its ACTUAL process (the ledger
// trail) and produces a ProcessTrace — the per-story QA view's data shape
// (sty_0915e1b7).
//
// It is a pure function over its inputs (story fields + parsed workflow +
// ledger entries), so it unit-tests without a DB and is reused by the portal
// view and the realtime stream. It is READ-ONLY: it never writes a ledger row
// or patches a status — it only reports what the loop already recorded.
package processtrace

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/workflow"
)

// LedgerEntry is the minimal projection of a ledger row the reconciler reads.
// Declared here (rather than importing internal/ledger) so this package — and
// the transports that use it — stay clear of the substrate-domain import ban
// (pr_mcp_cli_shared_path): callers map the ledger_list verb response into this
// shape, exactly as the portal already mirrors ledger rows for rendering.
type LedgerEntry struct {
	Kind      string
	Body      string
	Payload   json.RawMessage
	Actor     string
	CreatedAt time.Time
}

// Reviewer-enacted spine kinds. These are written by the gate skill itself via
// ledger_append (see document:project/reviewer-process), as literal strings —
// they are not Go constants in internal/ledger, which only names the kinds the
// verb layer writes. The reconciler matches on these literals.
const (
	kindReviewRequested  = "review_requested"
	kindReviewAccept     = "review_accept"
	kindReviewReject     = "review_reject"
	kindStatusTransition = "status_transition"
	kindStepSummary      = "step_summary"
)

// Actual-status values for a declared transition.
const (
	// StatusNotReached: the transition has not been attempted and is not the
	// immediate next step from the story's current status.
	StatusNotReached = "not_reached"
	// StatusPending: an outgoing transition from the current status that has
	// not yet fired — the loop's next move.
	StatusPending = "pending"
	// StatusRejected: the gate's last attempt on this transition rejected and
	// the status did not advance.
	StatusRejected = "rejected"
	// StatusAccepted: a gated transition that fired with an accept.
	StatusAccepted = "accepted"
	// StatusFired: an unguarded transition that fired.
	StatusFired = "fired"
)

// spinePayload is the union of the structured payload fields the reviewer
// spine rows carry. Each kind populates a subset; absent fields stay empty.
type spinePayload struct {
	FromStatus string `json:"from_status"`
	ToStatus   string `json:"to_status"`
	Gate       string `json:"gate"`
	GateSkill  string `json:"gate_skill"`
	Decision   string `json:"decision"`
}

// TransitionTrace is one declared transition annotated with what actually
// happened to it, per the ledger.
type TransitionTrace struct {
	From          string     `json:"from"`
	To            string     `json:"to"`
	ReviewerSkill string     `json:"reviewer_skill,omitempty"` // declared gate ("" = unguarded)
	Status        string     `json:"status"`                   // one of the Status* values
	Verdict       string     `json:"verdict,omitempty"`        // last accept/reject notes
	Actor         string     `json:"actor,omitempty"`
	At            *time.Time `json:"at,omitempty"`           // when it fired (status_transition)
	StepSummary   string     `json:"step_summary,omitempty"` // per-transition summary prose
	RejectCount   int        `json:"reject_count,omitempty"` // gate rejections before accept
}

// ProcessTrace is the reconciled declared-vs-actual view for one story.
type ProcessTrace struct {
	StoryID       string            `json:"story_id"`
	StoryType     string            `json:"story_type"`
	WorkflowName  string            `json:"workflow_name"`
	CurrentStatus string            `json:"current_status"`
	Transitions   []TransitionTrace `json:"transitions"`
}

// Reconcile overlays the actual ledger trail onto the declared workflow.
// Entries may arrive in any order; they are processed oldest-first so a
// later accept supersedes an earlier reject on a retried transition.
//
// Observability only (see principle process-as-configuration, "the boundary"):
// the workflow is parsed here to DISPLAY where a story sits, not to gate it.
func Reconcile(storyID, storyType, currentStatus string, wf *workflow.Workflow, entries []LedgerEntry) ProcessTrace {
	out := ProcessTrace{
		StoryID:       storyID,
		StoryType:     storyType,
		CurrentStatus: currentStatus,
	}
	if wf == nil {
		return out
	}
	out.WorkflowName = wf.Name

	sorted := append([]LedgerEntry(nil), entries...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].CreatedAt.Before(sorted[j].CreatedAt) })

	for _, tr := range wf.Transitions {
		tt := TransitionTrace{From: tr.From, To: tr.To, ReviewerSkill: tr.ReviewerSkill, Status: StatusNotReached}
		for _, e := range sorted {
			var p spinePayload
			if len(e.Payload) > 0 {
				_ = json.Unmarshal(e.Payload, &p)
			}
			switch e.Kind {
			case kindStatusTransition:
				if p.FromStatus == tr.From && p.ToStatus == tr.To {
					at := e.CreatedAt
					tt.At = &at
					tt.Actor = e.Actor
					if tr.ReviewerSkill == "" {
						tt.Status = StatusFired
					} else {
						tt.Status = StatusAccepted
					}
				}
			case kindReviewAccept:
				// Accept may omit to_status on some rows; key on from_status +
				// the gate (tolerant of naming drift) and on to_status when present.
				if p.FromStatus == tr.From && (p.ToStatus == "" || p.ToStatus == tr.To) && gateMatch(p.Gate, tr.ReviewerSkill) {
					tt.Status = StatusAccepted
					if strings.TrimSpace(e.Body) != "" {
						tt.Verdict = e.Body
					}
					if e.Actor != "" {
						tt.Actor = e.Actor
					}
				}
			case kindReviewReject:
				// Reject rows carry no to_status; disambiguate by gate.
				if p.FromStatus == tr.From && gateMatch(p.Gate, tr.ReviewerSkill) {
					tt.RejectCount++
					if tt.Status != StatusAccepted && tt.Status != StatusFired {
						tt.Status = StatusRejected
						if strings.TrimSpace(e.Body) != "" {
							tt.Verdict = e.Body
						}
						if e.Actor != "" {
							tt.Actor = e.Actor
						}
					}
				}
			case kindStepSummary:
				if p.FromStatus == tr.From && p.ToStatus == tr.To {
					tt.StepSummary = e.Body
				}
			}
		}
		// An untouched outgoing edge from the current status is the next move.
		if tt.Status == StatusNotReached && tr.From == currentStatus {
			tt.Status = StatusPending
		}
		out.Transitions = append(out.Transitions, tt)
	}
	return out
}

// gateMatch tolerates the two naming forms a gate appears under in the ledger
// (the bare "done-review" of older rows and the prefixed
// "satellites-story-done-review" of current ones). An empty declared gate or
// an empty payload gate is treated as a non-disqualifier — from_status (and
// to_status, where present) is the strong key; the gate only disambiguates two
// outgoing edges that share a from-state.
func gateMatch(payloadGate, declared string) bool {
	payloadGate = strings.TrimSpace(payloadGate)
	declared = strings.TrimSpace(declared)
	if declared == "" || payloadGate == "" {
		return true
	}
	return payloadGate == declared ||
		strings.HasSuffix(declared, payloadGate) ||
		strings.HasSuffix(payloadGate, declared)
}
