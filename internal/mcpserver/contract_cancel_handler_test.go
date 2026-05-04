package mcpserver

import (
	"encoding/json"
	"testing"

	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
)

// seedCIAtStatus writes a single CI for the fixture story directly
// through the contract store, then UpdateStatuses it through legal
// transitions to land at the target status. Used by contract_cancel
// tests that need a CI in a specific terminal/non-terminal state
// without booting the full claim/close lifecycle.
func (f *contractFixture) seedCIAtStatus(t *testing.T, contractName string, sequence int, target string) contract.ContractInstance {
	t.Helper()
	doc, err := f.server.docs.GetByName(f.ctx, "", contractName, nil)
	if err != nil {
		t.Fatalf("seedCIAtStatus: contract doc %q lookup: %v", contractName, err)
	}
	contractDocID := doc.ID
	_ = document.Store(nil) // import alive for future fixtures
	ci, err := f.server.contracts.Create(f.ctx, contract.ContractInstance{
		StoryID:          f.storyID,
		ContractID:       contractDocID,
		ContractName:     contractName,
		Sequence:         sequence,
		Status:           contract.StatusReady,
		RequiredForClose: true,
	}, f.now)
	if err != nil {
		t.Fatalf("seedCIAtStatus create: %v", err)
	}
	// Walk legal transitions to land at the target status.
	switch target {
	case contract.StatusReady:
		// already there
	case contract.StatusClaimed:
		ci, err = f.server.contracts.UpdateStatus(f.ctx, ci.ID, contract.StatusClaimed, f.caller.UserID, f.now, nil)
	case contract.StatusPendingReview:
		ci, err = f.server.contracts.UpdateStatus(f.ctx, ci.ID, contract.StatusClaimed, f.caller.UserID, f.now, nil)
		if err == nil {
			ci, err = f.server.contracts.UpdateStatus(f.ctx, ci.ID, contract.StatusPendingReview, f.caller.UserID, f.now, nil)
		}
	case contract.StatusPassed:
		ci, err = f.server.contracts.UpdateStatus(f.ctx, ci.ID, contract.StatusClaimed, f.caller.UserID, f.now, nil)
		if err == nil {
			ci, err = f.server.contracts.UpdateStatus(f.ctx, ci.ID, contract.StatusPassed, f.caller.UserID, f.now, nil)
		}
	case contract.StatusFailed:
		ci, err = f.server.contracts.UpdateStatus(f.ctx, ci.ID, contract.StatusClaimed, f.caller.UserID, f.now, nil)
		if err == nil {
			ci, err = f.server.contracts.UpdateStatus(f.ctx, ci.ID, contract.StatusFailed, f.caller.UserID, f.now, nil)
		}
	case contract.StatusSkipped:
		ci, err = f.server.contracts.UpdateStatus(f.ctx, ci.ID, contract.StatusSkipped, f.caller.UserID, f.now, nil)
	default:
		t.Fatalf("seedCIAtStatus: unsupported target status %q", target)
	}
	if err != nil {
		t.Fatalf("seedCIAtStatus walk to %q: %v", target, err)
	}
	return ci
}

// callContractCancel invokes the handler with the standard caller and
// returns the parsed response body or the raw text on isError.
func (f *contractFixture) callContractCancel(t *testing.T, ciID, reason string) (map[string]any, string, bool) {
	t.Helper()
	res, err := f.server.handleContractCancel(f.callerCtx(), newCallToolReq("contract_cancel", map[string]any{
		"contract_instance_id": ciID,
		"reason":               reason,
	}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	text := firstText(res)
	if res.IsError {
		return nil, text, true
	}
	out := map[string]any{}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("parse response: %v (text=%s)", err, text)
	}
	return out, text, false
}

// TestContractCancel_FromFailed_MintsSuccessor — happy path on a
// terminal failed CI. Original CI stays at failed (audit invariant);
// successor minted at status=ready with PriorCIID set.
func TestContractCancel_FromFailed_MintsSuccessor(t *testing.T) {
	t.Parallel()
	f := newContractFixture(t)
	prior := f.seedCIAtStatus(t, "plan", 0, contract.StatusFailed)

	body, text, isErr := f.callContractCancel(t, prior.ID, "iteration cap exhausted")
	if isErr {
		t.Fatalf("unexpected isError: %s", text)
	}
	if body["prior_status"] != contract.StatusFailed {
		t.Fatalf("prior_status: got %v want %q", body["prior_status"], contract.StatusFailed)
	}
	successorID, _ := body["successor_ci_id"].(string)
	if successorID == "" {
		t.Fatalf("expected successor_ci_id, got %s", text)
	}

	// Original CI stays failed.
	reread, err := f.server.contracts.GetByID(f.ctx, prior.ID, nil)
	if err != nil {
		t.Fatalf("re-read prior: %v", err)
	}
	if reread.Status != contract.StatusFailed {
		t.Fatalf("prior CI flipped from failed to %q — audit invariant violated", reread.Status)
	}

	// Successor in ready with PriorCIID pointing at the cancelled CI.
	successor, err := f.server.contracts.GetByID(f.ctx, successorID, nil)
	if err != nil {
		t.Fatalf("re-read successor: %v", err)
	}
	if successor.Status != contract.StatusReady {
		t.Fatalf("successor status: got %q want %q", successor.Status, contract.StatusReady)
	}
	if successor.PriorCIID != prior.ID {
		t.Fatalf("successor PriorCIID: got %q want %q", successor.PriorCIID, prior.ID)
	}
	if successor.ContractName != prior.ContractName {
		t.Fatalf("successor ContractName: got %q want %q", successor.ContractName, prior.ContractName)
	}
	if successor.Sequence != prior.Sequence {
		t.Fatalf("successor Sequence: got %d want %d", successor.Sequence, prior.Sequence)
	}
}

// TestContractCancel_FromClaimed_FlipsToCancelledAndMints — non-terminal
// claimed CI flips to cancelled (terminal) and a successor is minted.
func TestContractCancel_FromClaimed_FlipsToCancelledAndMints(t *testing.T) {
	t.Parallel()
	f := newContractFixture(t)
	prior := f.seedCIAtStatus(t, "plan", 0, contract.StatusClaimed)

	body, text, isErr := f.callContractCancel(t, prior.ID, "operator abort")
	if isErr {
		t.Fatalf("unexpected isError: %s", text)
	}
	if body["prior_status"] != contract.StatusClaimed {
		t.Fatalf("prior_status: got %v want %q", body["prior_status"], contract.StatusClaimed)
	}
	if body["successor_ci_id"] == "" {
		t.Fatalf("expected successor_ci_id, got %s", text)
	}

	reread, _ := f.server.contracts.GetByID(f.ctx, prior.ID, nil)
	if reread.Status != contract.StatusCancelled {
		t.Fatalf("prior CI status: got %q want %q (claimed should flip to cancelled)", reread.Status, contract.StatusCancelled)
	}
}

// TestContractCancel_FromPendingReview_FlipsToCancelledAndMints
func TestContractCancel_FromPendingReview_FlipsToCancelledAndMints(t *testing.T) {
	t.Parallel()
	f := newContractFixture(t)
	prior := f.seedCIAtStatus(t, "plan", 0, contract.StatusPendingReview)

	_, text, isErr := f.callContractCancel(t, prior.ID, "reviewer stuck")
	if isErr {
		t.Fatalf("unexpected isError: %s", text)
	}
	reread, _ := f.server.contracts.GetByID(f.ctx, prior.ID, nil)
	if reread.Status != contract.StatusCancelled {
		t.Fatalf("prior CI status: got %q want %q", reread.Status, contract.StatusCancelled)
	}
}

// TestContractCancel_FromPassed_PreservesAuditAndMints — cancelling a
// passed CI leaves it at passed (audit) and mints a successor.
func TestContractCancel_FromPassed_PreservesAuditAndMints(t *testing.T) {
	t.Parallel()
	f := newContractFixture(t)
	prior := f.seedCIAtStatus(t, "plan", 0, contract.StatusPassed)

	body, text, isErr := f.callContractCancel(t, prior.ID, "operator retracts")
	if isErr {
		t.Fatalf("unexpected isError: %s", text)
	}
	if body["prior_status"] != contract.StatusPassed {
		t.Fatalf("prior_status: got %v want %q", body["prior_status"], contract.StatusPassed)
	}

	reread, _ := f.server.contracts.GetByID(f.ctx, prior.ID, nil)
	if reread.Status != contract.StatusPassed {
		t.Fatalf("prior CI status: got %q want %q (passed should stay passed)", reread.Status, contract.StatusPassed)
	}
}

// TestContractCancel_FromReady_RejectsCiNotCancellable — a ready CI is
// not a meaningful target for cancellation; the verb should reject.
func TestContractCancel_FromReady_RejectsCiNotCancellable(t *testing.T) {
	t.Parallel()
	f := newContractFixture(t)
	prior := f.seedCIAtStatus(t, "plan", 0, contract.StatusReady)

	_, text, isErr := f.callContractCancel(t, prior.ID, "shouldn't work")
	if !isErr {
		t.Fatalf("expected isError for ready CI, got %s", text)
	}
	if !anySubstring(text, `"error":"ci_not_cancellable"`, `"status":"ready"`) {
		t.Fatalf("expected structured ci_not_cancellable, got %s", text)
	}
}

// TestContractCancel_FromSkipped_RejectsCiNotCancellable — skipped is
// already a terminal state with no recovery semantics.
func TestContractCancel_FromSkipped_RejectsCiNotCancellable(t *testing.T) {
	t.Parallel()
	f := newContractFixture(t)
	prior := f.seedCIAtStatus(t, "plan", 0, contract.StatusSkipped)

	_, text, isErr := f.callContractCancel(t, prior.ID, "shouldn't work")
	if !isErr {
		t.Fatalf("expected isError for skipped CI, got %s", text)
	}
	if !anySubstring(text, `"error":"ci_not_cancellable"`, `"status":"skipped"`) {
		t.Fatalf("expected structured ci_not_cancellable, got %s", text)
	}
}

// TestContractCancel_CIIDNotFound — unknown CI id returns ci_not_found.
func TestContractCancel_CIIDNotFound(t *testing.T) {
	t.Parallel()
	f := newContractFixture(t)
	_, text, isErr := f.callContractCancel(t, "ci_doesnotexist", "missing")
	if !isErr {
		t.Fatalf("expected isError, got %s", text)
	}
	if !anySubstring(text, `"error":"ci_not_found"`) {
		t.Fatalf("expected ci_not_found, got %s", text)
	}
}

// TestContractCancel_AppendsKindCancellationLedgerRow — the kind:
// cancellation row carries prior_ci_id + reason in the structured payload.
func TestContractCancel_AppendsKindCancellationLedgerRow(t *testing.T) {
	t.Parallel()
	f := newContractFixture(t)
	prior := f.seedCIAtStatus(t, "plan", 0, contract.StatusFailed)

	body, _, isErr := f.callContractCancel(t, prior.ID, "audit-trail check")
	if isErr {
		t.Fatalf("unexpected isError")
	}
	rowID, _ := body["cancellation_ledger_id"].(string)
	if rowID == "" {
		t.Fatalf("expected cancellation_ledger_id in response")
	}
	rows, err := f.server.ledger.List(f.ctx, f.projectID, ledger.ListOptions{
		Tags: []string{"kind:cancellation"},
	}, nil)
	if err != nil {
		t.Fatalf("ledger list: %v", err)
	}
	var match *ledger.LedgerEntry
	for i := range rows {
		if rows[i].ID == rowID {
			match = &rows[i]
			break
		}
	}
	if match == nil {
		t.Fatalf("cancellation row %s not found in ledger", rowID)
	}
	var payload map[string]any
	if err := json.Unmarshal(match.Structured, &payload); err != nil {
		t.Fatalf("structured payload parse: %v", err)
	}
	if payload["prior_ci_id"] != prior.ID {
		t.Fatalf("prior_ci_id: got %v want %q", payload["prior_ci_id"], prior.ID)
	}
	if payload["reason"] != "audit-trail check" {
		t.Fatalf("reason: got %v want %q", payload["reason"], "audit-trail check")
	}
}

// TestContractCancel_SuccessorIsClaimable_PredecessorGatePasses — after
// cancellation, the successor CI passes the predecessor gate (slot
// relief works as planned).
func TestContractCancel_SuccessorIsClaimable_PredecessorGatePasses(t *testing.T) {
	t.Parallel()
	f := newContractFixture(t)
	prior := f.seedCIAtStatus(t, "plan", 0, contract.StatusFailed)

	body, _, isErr := f.callContractCancel(t, prior.ID, "test gate")
	if isErr {
		t.Fatalf("unexpected isError")
	}
	successorID, _ := body["successor_ci_id"].(string)

	// Verify predecessor gate accepts the successor.
	peers, err := f.server.contracts.List(f.ctx, f.storyID, nil)
	if err != nil {
		t.Fatalf("list peers: %v", err)
	}
	var successor contract.ContractInstance
	for _, p := range peers {
		if p.ID == successorID {
			successor = p
			break
		}
	}
	if successor.ID == "" {
		t.Fatalf("successor not found in peer list")
	}
	if err := contract.PredecessorGate(peers, successor); err != nil {
		t.Fatalf("predecessor gate rejected successor: %v", err)
	}
}
