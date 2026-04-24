package contract

import (
	"errors"
	"testing"
)

func ci(id string, seq int, required bool, status string) ContractInstance {
	return ContractInstance{ID: id, Sequence: seq, RequiredForClose: required, Status: status}
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
