package contract

import "fmt"

// GateRejection is a structured error the process-order gate returns
// when a claim is blocked. It surfaces enough fields for the handler
// to render a JSON-shaped tool-result body.
type GateRejection struct {
	Kind     string // ci_not_ready | predecessor_not_terminal | wrong_session | ci_not_found
	CIID     string // the CI being claimed (on ci_not_ready / wrong_session)
	Blocking string // the blocking predecessor id (predecessor_not_terminal)
	Current  string // the CI's current status (for ci_not_ready / predecessor)
}

// Error implements error with a low-cardinality message. Fields carry
// the high-cardinality detail.
func (e *GateRejection) Error() string {
	switch e.Kind {
	case "ci_not_ready":
		return fmt.Sprintf("contract: CI not ready (status=%q)", e.Current)
	case "predecessor_not_terminal":
		return fmt.Sprintf("contract: predecessor %q not terminal (status=%q)", e.Blocking, e.Current)
	case "wrong_session":
		return fmt.Sprintf("contract: CI %q already claimed by another session", e.CIID)
	case "ci_not_found":
		return fmt.Sprintf("contract: CI %q not found", e.CIID)
	default:
		return "contract: claim rejected"
	}
}

// PredecessorGate enforces the "all required_for_close predecessors
// must be terminal" invariant. Returns nil when target can be claimed.
// peers is the full CI list for target's story; sequence ordering is
// decided by slot, not by peers slice order.
func PredecessorGate(peers []ContractInstance, target ContractInstance) error {
	for _, p := range peers {
		if p.ID == target.ID {
			continue
		}
		if p.Sequence >= target.Sequence {
			continue
		}
		if !p.RequiredForClose {
			continue
		}
		if p.Status == StatusPassed || p.Status == StatusSkipped {
			continue
		}
		return &GateRejection{Kind: "predecessor_not_terminal", Blocking: p.ID, Current: p.Status}
	}
	return nil
}

// CheckCIReady rejects claims on CIs that are not in the ready state,
// unless the same session is amending (that branch bypasses this
// function and heads down the amend path).
func CheckCIReady(ci ContractInstance) error {
	if ci.Status == StatusReady {
		return nil
	}
	return &GateRejection{Kind: "ci_not_ready", CIID: ci.ID, Current: ci.Status}
}
