package contract

import "fmt"

// GateRejection is a structured error the process-order gate returns
// when a claim is blocked. It surfaces enough fields for the handler
// to render a JSON-shaped tool-result body.
type GateRejection struct {
	Kind     string // ci_not_ready | predecessor_not_terminal | grant_mismatch | ci_not_found
	CIID     string // the CI being claimed (on ci_not_ready / grant_mismatch)
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
	case "grant_mismatch":
		return fmt.Sprintf("contract: CI %q already claimed under a different grant", e.CIID)
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
//
// Iteration-loop awareness (sty_c3bd5e6c): a `failed` CI is treated as
// relieved when a peer with the same `(ContractName, Sequence)` exists
// in `passed` or `skipped`. This is the rejection-append loop: when a
// review rejects iteration N, CommitReviewVerdict creates iteration
// N+1 at the same slot with the same sequence + a PriorCIID pointer.
// Without this carve-out, a single review rejection on a
// required_for_close slot would permanently block every downstream CI.
//
// Cancellation-loop awareness (sty_3a59a6d7): a `cancelled` CI mirrors
// `failed` for slot-relief purposes. contract_cancel always mints a
// successor at the same slot (PriorCIID set), so the cancelled peer
// can be ignored once the successor reaches passed/skipped.
func PredecessorGate(peers []ContractInstance, target ContractInstance) error {
	relieved := slotRelieved(peers)
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
		if (p.Status == StatusFailed || p.Status == StatusCancelled) && relieved[slotKey{Name: p.ContractName, Sequence: p.Sequence}] {
			// A passed/skipped iteration successor exists at the same
			// slot — the loop is resolved at this position.
			continue
		}
		return &GateRejection{Kind: "predecessor_not_terminal", Blocking: p.ID, Current: p.Status}
	}
	return nil
}

// slotKey identifies a workflow slot — a (ContractName, Sequence)
// pair. Multiple CIs share the same slotKey when the rejection-append
// loop produces iteration N+1 at the prior CI's slot.
type slotKey struct {
	Name     string
	Sequence int
}

// slotRelieved returns the set of slots for which at least one peer is
// in `passed` or `skipped`. Used by PredecessorGate to ignore failed
// peers whose loop has since produced a passing successor.
func slotRelieved(peers []ContractInstance) map[slotKey]bool {
	out := map[slotKey]bool{}
	for _, p := range peers {
		if p.Status == StatusPassed || p.Status == StatusSkipped {
			out[slotKey{Name: p.ContractName, Sequence: p.Sequence}] = true
		}
	}
	return out
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
