package contract

import (
	"errors"
	"testing"
)

func ci(id string, seq int, required bool, status string) ContractInstance {
	return ContractInstance{ID: id, Sequence: seq, RequiredForClose: required, Status: status}
}

// ciNamed adds the contract_name needed by the iteration-loop
// gate-relief logic. Existing tests use blank ContractName; tests for
// sty_c3bd5e6c set it explicitly.
func ciNamed(id, name string, seq int, required bool, status string) ContractInstance {
	return ContractInstance{ID: id, ContractName: name, Sequence: seq, RequiredForClose: required, Status: status}
}

func TestPredecessorGate_AllTerminal(t *testing.T) {
	t.Parallel()
	peers := []ContractInstance{
		ci("a", 0, true, StatusPassed),
		ci("b", 1, true, StatusPassed),
		ci("c", 2, true, StatusReady),
	}
	if err := PredecessorGate(peers, peers[2]); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestPredecessorGate_SkippedCountsAsTerminal(t *testing.T) {
	t.Parallel()
	peers := []ContractInstance{
		ci("a", 0, true, StatusSkipped),
		ci("b", 1, true, StatusReady),
	}
	if err := PredecessorGate(peers, peers[1]); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestPredecessorGate_NonRequiredIgnored(t *testing.T) {
	t.Parallel()
	peers := []ContractInstance{
		ci("a", 0, false, StatusReady), // non-required; status=ready is OK
		ci("b", 1, true, StatusReady),
	}
	if err := PredecessorGate(peers, peers[1]); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestPredecessorGate_Blocked(t *testing.T) {
	t.Parallel()
	peers := []ContractInstance{
		ci("a", 0, true, StatusPassed),
		ci("b", 1, true, StatusReady), // predecessor still ready
		ci("c", 2, true, StatusReady),
	}
	err := PredecessorGate(peers, peers[2])
	if err == nil {
		t.Fatalf("expected rejection")
	}
	var gr *GateRejection
	if !errors.As(err, &gr) {
		t.Fatalf("expected *GateRejection, got %T", err)
	}
	if gr.Kind != "predecessor_not_terminal" {
		t.Fatalf("kind: %q", gr.Kind)
	}
	if gr.Blocking != "b" {
		t.Fatalf("blocking: %q", gr.Blocking)
	}
	if gr.Current != StatusReady {
		t.Fatalf("current: %q", gr.Current)
	}
}

// TestPredecessorGate_FailedIterationRelievedByPassedSuccessor verifies
// sty_c3bd5e6c: a failed iteration-(N-1) CI must not block downstream
// claims when a passed iteration-N CI exists at the same workflow slot
// (same ContractName + Sequence). The rejection-append loop's slot-
// level resolution is what matters; the orphaned failed iteration
// stays in the audit trail without gating the workflow.
func TestPredecessorGate_FailedIterationRelievedByPassedSuccessor(t *testing.T) {
	t.Parallel()
	peers := []ContractInstance{
		ciNamed("ci_plan_v1", "plan", 0, true, StatusFailed),
		ciNamed("ci_plan_v2", "plan", 0, true, StatusPassed),
		ciNamed("ci_develop", "develop", 1, true, StatusReady),
	}
	if err := PredecessorGate(peers, peers[2]); err != nil {
		t.Fatalf("expected nil — failed iteration relieved by passed successor at slot, got %v", err)
	}
}

// TestPredecessorGate_FailedWithNoPassedSuccessor confirms the loop-
// still-in-flight case still blocks: a failed CI with no passed peer
// at the same slot continues to gate downstream claims (the iteration
// loop is open, not resolved).
func TestPredecessorGate_FailedWithNoPassedSuccessor(t *testing.T) {
	t.Parallel()
	peers := []ContractInstance{
		ciNamed("ci_plan_v1", "plan", 0, true, StatusFailed),
		// No iteration-2 CI yet — the loop is still in flight.
		ciNamed("ci_develop", "develop", 1, true, StatusReady),
	}
	err := PredecessorGate(peers, peers[1])
	if err == nil {
		t.Fatalf("expected rejection — failed predecessor with no successor must still block")
	}
	var gr *GateRejection
	if !errors.As(err, &gr) || gr.Kind != "predecessor_not_terminal" {
		t.Fatalf("expected predecessor_not_terminal, got %v", err)
	}
	if gr.Blocking != "ci_plan_v1" {
		t.Fatalf("blocking: %q", gr.Blocking)
	}
}

// TestPredecessorGate_FailedDifferentSlotStillBlocks confirms the slot
// match must include both ContractName AND Sequence. A passed CI on a
// different slot does not relieve a failed CI on a separate slot.
func TestPredecessorGate_FailedDifferentSlotStillBlocks(t *testing.T) {
	t.Parallel()
	peers := []ContractInstance{
		ciNamed("ci_plan_v1", "plan", 0, true, StatusFailed),
		// Passed CI is on a different slot (different name) — does NOT
		// relieve the plan-slot failure.
		ciNamed("ci_other", "different", 0, true, StatusPassed),
		ciNamed("ci_develop", "develop", 1, true, StatusReady),
	}
	err := PredecessorGate(peers, peers[2])
	if err == nil {
		t.Fatalf("expected rejection — failed plan must block when only a different slot passed")
	}
	var gr *GateRejection
	if !errors.As(err, &gr) || gr.Kind != "predecessor_not_terminal" {
		t.Fatalf("expected predecessor_not_terminal, got %v", err)
	}
	if gr.Blocking != "ci_plan_v1" {
		t.Fatalf("blocking: %q", gr.Blocking)
	}
}

func TestCheckCIReady(t *testing.T) {
	t.Parallel()
	if err := CheckCIReady(ci("x", 0, true, StatusReady)); err != nil {
		t.Fatalf("ready should pass: %v", err)
	}
	err := CheckCIReady(ci("x", 0, true, StatusClaimed))
	if err == nil {
		t.Fatalf("claimed should reject")
	}
	var gr *GateRejection
	if !errors.As(err, &gr) || gr.Kind != "ci_not_ready" {
		t.Fatalf("expected ci_not_ready, got %v", err)
	}
	if gr.Current != StatusClaimed {
		t.Fatalf("current: %q", gr.Current)
	}
}
