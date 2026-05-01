package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/contract"
)

// claimDefaultWorkflow is a shared helper that locks the default
// plan/develop/story_close workflow on the fixture story.
func claimDefaultWorkflow(t *testing.T, f *contractFixture) []contract.ContractInstance {
	t.Helper()
	res, err := f.server.handleWorkflowClaim(f.callerCtx(), newCallToolReq("workflow_claim", map[string]any{
		"story_id":           f.storyID,
		"proposed_contracts": []string{"plan", "develop", "story_close"},
		"claim_markdown":     "shape-approved",
	}))
	if err != nil || res.IsError {
		t.Fatalf("workflow_claim: err=%v isError=%v body=%s", err, res.IsError, firstText(res))
	}
	var body struct {
		ContractInstances []contract.ContractInstance `json:"contract_instances"`
	}
	if err := json.Unmarshal([]byte(firstText(res)), &body); err != nil {
		t.Fatalf("parse: %v", err)
	}
	return body.ContractInstances
}

// findCI is a convenience for tests asserting on a particular CI slot.
func findCI(cis []contract.ContractInstance, name string) (contract.ContractInstance, bool) {
	for _, ci := range cis {
		if ci.ContractName == name {
			return ci, true
		}
	}
	return contract.ContractInstance{}, false
}

// TestPlanAmend_HappyPath drives a workflow claim then amends a second
// develop CI scoped to AC=[2] under the parent develop CI. Asserts the
// new CI is created with the right scope + parent + sequence and that a
// kind:plan-amend ledger row is written.
func TestPlanAmend_HappyPath(t *testing.T) {
	t.Parallel()
	f := newContractFixture(t)
	cis := claimDefaultWorkflow(t, f)
	dev, ok := findCI(cis, "develop")
	if !ok {
		t.Fatalf("develop CI missing")
	}
	addsJSON, _ := json.Marshal([]planAmendInvocation{
		{ContractName: "develop", ACScope: []int{2}, ParentInvocationID: dev.ID},
	})
	res, err := f.server.handlePlanAmend(f.callerCtx(), newCallToolReq("plan_amend", map[string]any{
		"story_id":        f.storyID,
		"add_invocations": string(addsJSON),
		"reason":          "AC 2 needs rework after develop close",
	}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if res.IsError {
		t.Fatalf("isError: %s", firstText(res))
	}
	var body struct {
		StoryID           string                      `json:"story_id"`
		PlanAmendLedgerID string                      `json:"plan_amend_ledger_id"`
		ContractInstances []contract.ContractInstance `json:"contract_instances"`
	}
	if err := json.Unmarshal([]byte(firstText(res)), &body); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if body.PlanAmendLedgerID == "" {
		t.Fatalf("plan_amend_ledger_id empty")
	}
	if len(body.ContractInstances) != 1 {
		t.Fatalf("created CI count: got %d want 1", len(body.ContractInstances))
	}
	added := body.ContractInstances[0]
	if added.ContractName != "develop" {
		t.Errorf("contract_name = %q, want develop", added.ContractName)
	}
	if len(added.ACScope) != 1 || added.ACScope[0] != 2 {
		t.Errorf("ac_scope = %v, want [2]", added.ACScope)
	}
	if added.ParentInvocationID != dev.ID {
		t.Errorf("parent_invocation_id = %q, want %q", added.ParentInvocationID, dev.ID)
	}
	if added.Sequence != len(cis) {
		t.Errorf("sequence = %d, want %d (after the existing %d CIs)", added.Sequence, len(cis), len(cis))
	}
	if added.Status != contract.StatusReady {
		t.Errorf("status = %q, want ready", added.Status)
	}
}

// TestPlanAmend_SlotViolation deleted by epic:configuration-over-code-mandate
// (story_af79cf95) — substrate slot algebra (count_out_of_range) is
// gone. The reviewer agent (story_reviewer / development_reviewer)
// judges whether amended contracts are appropriate.

// TestPlanAmend_ACCapExceeded rejects re-amending the same AC past the
// configured cap.
func TestPlanAmend_ACCapExceeded(t *testing.T) {
	t.Setenv("SATELLITES_MAX_AC_ITERATIONS", "1")
	f := newContractFixture(t)
	cis := claimDefaultWorkflow(t, f)
	dev, _ := findCI(cis, "develop")

	addsJSON, _ := json.Marshal([]planAmendInvocation{
		{ContractName: "develop", ACScope: []int{2}, ParentInvocationID: dev.ID},
	})
	first, _ := f.server.handlePlanAmend(f.callerCtx(), newCallToolReq("plan_amend", map[string]any{
		"story_id":        f.storyID,
		"add_invocations": string(addsJSON),
		"reason":          "first amend",
	}))
	if first.IsError {
		t.Fatalf("first amend should not error against cap=1 (existing has 0 amended-AC iterations); got %s", firstText(first))
	}
	second, _ := f.server.handlePlanAmend(f.callerCtx(), newCallToolReq("plan_amend", map[string]any{
		"story_id":        f.storyID,
		"add_invocations": string(addsJSON),
		"reason":          "second amend should hit cap",
	}))
	text := firstText(second)
	if !strings.Contains(text, "ac_iteration_cap_exceeded") {
		t.Errorf("expected ac_iteration_cap_exceeded; got %s", text)
	}
}

// TestPlanAmend_UnknownStory rejects with a clean message.
func TestPlanAmend_UnknownStory(t *testing.T) {
	t.Parallel()
	f := newContractFixture(t)
	addsJSON, _ := json.Marshal([]planAmendInvocation{
		{ContractName: "develop"},
	})
	res, _ := f.server.handlePlanAmend(f.callerCtx(), newCallToolReq("plan_amend", map[string]any{
		"story_id":        "story_does_not_exist",
		"add_invocations": string(addsJSON),
		"reason":          "x",
	}))
	if !strings.Contains(firstText(res), "story not found") {
		t.Errorf("expected story-not-found error; got %s", firstText(res))
	}
}

// TestPlanAmend_UnknownParent rejects when parent_invocation_id points
// at a CI that doesn't belong to the story.
func TestPlanAmend_UnknownParent(t *testing.T) {
	t.Parallel()
	f := newContractFixture(t)
	_ = claimDefaultWorkflow(t, f)
	addsJSON, _ := json.Marshal([]planAmendInvocation{
		{ContractName: "develop", ParentInvocationID: "ci_does_not_exist"},
	})
	res, _ := f.server.handlePlanAmend(f.callerCtx(), newCallToolReq("plan_amend", map[string]any{
		"story_id":        f.storyID,
		"add_invocations": string(addsJSON),
		"reason":          "bad parent",
	}))
	if !strings.Contains(firstText(res), "unknown_parent_invocation") {
		t.Errorf("expected unknown_parent_invocation; got %s", firstText(res))
	}
}

// TestPlanAmend_RequiresInitialPlan rejects amend before workflow_claim.
func TestPlanAmend_RequiresInitialPlan(t *testing.T) {
	t.Parallel()
	f := newContractFixture(t)
	addsJSON, _ := json.Marshal([]planAmendInvocation{
		{ContractName: "develop"},
	})
	res, _ := f.server.handlePlanAmend(f.callerCtx(), newCallToolReq("plan_amend", map[string]any{
		"story_id":        f.storyID,
		"add_invocations": string(addsJSON),
		"reason":          "amend before claim",
	}))
	text := firstText(res)
	if !strings.Contains(text, "workflow_claim") {
		t.Errorf("expected message directing to workflow_claim; got %s", text)
	}
}
