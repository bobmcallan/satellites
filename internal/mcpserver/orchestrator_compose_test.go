package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/task"
)

// orchestratorFixture extends the contract fixture with a system
// workflow Document + matching system agents + a task store, so the
// orchestrator compose path has all four input layers populated.
type orchestratorFixture struct {
	*contractFixture
	taskStore task.Store
}

func newOrchestratorFixture(t *testing.T) *orchestratorFixture {
	t.Helper()
	f := newContractFixture(t)

	// Wire a task store onto the existing server. The contract fixture
	// builds its server without a task store; orchestrator compose
	// needs one to enqueue per-slot tasks.
	taskStore := task.NewMemoryStore()
	f.server.tasks = taskStore

	// Seed the system workflow Document (4-slot default mirroring
	// the contract fixture's contracts).
	systemSrc := document.Document{
		Type:   document.TypeWorkflow,
		Scope:  document.ScopeSystem,
		Name:   "default",
		Body:   "system workflow",
		Status: document.StatusActive,
		Structured: []byte(`{"required_slots":[
			{"contract_name":"preplan","required":true,"min_count":1,"max_count":1},
			{"contract_name":"plan","required":true,"min_count":1,"max_count":1},
			{"contract_name":"develop","required":true,"min_count":1,"max_count":10},
			{"contract_name":"story_close","required":true,"min_count":1,"max_count":1}
		]}`),
	}
	if _, err := f.server.docs.Create(context.Background(), systemSrc, f.now); err != nil {
		t.Fatalf("seed system workflow: %v", err)
	}

	// Seed system agents matching the role-mapping convention
	// (story_87b46d01): one row per role agent, multiple contracts
	// resolve to the same row.
	for _, name := range []string{"developer_agent", "releaser_agent", "story_close_agent"} {
		settings, _ := document.MarshalAgentSettings(document.AgentSettings{
			PermissionPatterns: []string{"Read:**"},
		})
		if _, err := f.server.docs.Create(context.Background(), document.Document{
			Type:       document.TypeAgent,
			Scope:      document.ScopeSystem,
			Name:       name,
			Body:       "agent body for " + name,
			Status:     document.StatusActive,
			Structured: settings,
		}, f.now); err != nil {
			t.Fatalf("seed agent %q: %v", name, err)
		}
	}

	return &orchestratorFixture{contractFixture: f, taskStore: taskStore}
}

// TestOrchestratorComposePlan_EndToEnd covers AC2-AC5 of story_66d4249f.
// Invokes the new MCP verb against a fresh story and asserts:
//
//   - one task is enqueued per resolved slot, in sequence order;
//   - each task's payload carries {contract_name, agent_ref, sequence};
//   - one kind:plan ledger row is written, with id appearing before the
//     kind:workflow-claim row;
//   - workflow_claim succeeded — CIs equal the proposed list.
func TestOrchestratorComposePlan_EndToEnd(t *testing.T) {
	t.Parallel()
	f := newOrchestratorFixture(t)

	res, err := f.server.handleOrchestratorComposePlan(f.callerCtx(), newCallToolReq("orchestrator_compose_plan", map[string]any{
		"story_id": f.storyID,
	}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if res.IsError {
		t.Fatalf("handler returned error: %s", firstText(res))
	}
	var body struct {
		StoryID               string                      `json:"story_id"`
		PlanLedgerID          string                      `json:"plan_ledger_id"`
		WorkflowClaimLedgerID string                      `json:"workflow_claim_ledger_id"`
		TaskIDs               []string                    `json:"task_ids"`
		ProposedContracts     []string                    `json:"proposed_contracts"`
		AgentAssignments      map[string]string           `json:"agent_assignments"`
		ContractInstances     []contract.ContractInstance `json:"contract_instances"`
	}
	if err := json.Unmarshal([]byte(firstText(res)), &body); err != nil {
		t.Fatalf("parse response: %v", err)
	}

	// Proposed list = the four resolved slots.
	wantNames := []string{"preplan", "plan", "develop", "story_close"}
	if len(body.ProposedContracts) != len(wantNames) {
		t.Fatalf("proposed_contracts len: got %d want %d (%v)", len(body.ProposedContracts), len(wantNames), body.ProposedContracts)
	}
	for i, name := range wantNames {
		if body.ProposedContracts[i] != name {
			t.Fatalf("proposed[%d]: got %q want %q", i, body.ProposedContracts[i], name)
		}
	}

	// AC3 — tasks enqueued, payload shape correct.
	if len(body.TaskIDs) != len(wantNames) {
		t.Fatalf("task_ids len: got %d want %d", len(body.TaskIDs), len(wantNames))
	}
	for i, taskID := range body.TaskIDs {
		gotTask, gerr := f.taskStore.GetByID(context.Background(), taskID, []string{f.wsID})
		if gerr != nil {
			t.Fatalf("task[%d] %s: %v", i, taskID, gerr)
		}
		if gotTask.Origin != task.OriginStoryStage {
			t.Errorf("task[%d] origin: got %q want %q", i, gotTask.Origin, task.OriginStoryStage)
		}
		var payload orchestratorTaskPayload
		if err := json.Unmarshal(gotTask.Payload, &payload); err != nil {
			t.Fatalf("task[%d] payload parse: %v", i, err)
		}
		if payload.ContractName != wantNames[i] {
			t.Errorf("task[%d] contract_name: got %q want %q", i, payload.ContractName, wantNames[i])
		}
		if payload.Sequence != i {
			t.Errorf("task[%d] sequence: got %d want %d", i, payload.Sequence, i)
		}
		if payload.AgentRef == "" {
			t.Errorf("task[%d] agent_ref empty; expected matching <contract>_agent doc", i)
		}
		if payload.StoryID != f.storyID {
			t.Errorf("task[%d] story_id: got %q want %q", i, payload.StoryID, f.storyID)
		}
	}

	// AC4 — kind:plan ledger row exists and precedes the kind:workflow-claim row.
	planRows, err := f.server.ledger.List(f.ctx, f.projectID, ledger.ListOptions{
		Type: ledger.TypePlan,
		Tags: []string{"kind:plan"},
	}, nil)
	if err != nil {
		t.Fatalf("list plan rows: %v", err)
	}
	if len(planRows) != 1 {
		t.Fatalf("kind:plan rows: got %d want 1", len(planRows))
	}
	if planRows[0].ID != body.PlanLedgerID {
		t.Errorf("plan ledger id mismatch: got %q in response, %q in store", body.PlanLedgerID, planRows[0].ID)
	}
	claimRows, err := f.server.ledger.List(f.ctx, f.projectID, ledger.ListOptions{
		Type: ledger.TypeWorkflowClaim,
	}, nil)
	if err != nil {
		t.Fatalf("list claim rows: %v", err)
	}
	if len(claimRows) != 1 {
		t.Fatalf("kind:workflow-claim rows: got %d want 1", len(claimRows))
	}
	if !planRows[0].CreatedAt.Before(claimRows[0].CreatedAt) && !planRows[0].CreatedAt.Equal(claimRows[0].CreatedAt) {
		t.Errorf("plan row should not be after workflow-claim row: plan=%v claim=%v", planRows[0].CreatedAt, claimRows[0].CreatedAt)
	}

	// CIs created equal proposed.
	if len(body.ContractInstances) != len(wantNames) {
		t.Fatalf("CIs len: got %d want %d", len(body.ContractInstances), len(wantNames))
	}
}

// TestOrchestratorComposePlan_Idempotent verifies the second invocation
// returns the existing CIs without writing a new plan or duplicating
// tasks.
func TestOrchestratorComposePlan_Idempotent(t *testing.T) {
	t.Parallel()
	f := newOrchestratorFixture(t)
	args := map[string]any{"story_id": f.storyID}

	first, err := f.server.handleOrchestratorComposePlan(f.callerCtx(), newCallToolReq("orchestrator_compose_plan", args))
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if first.IsError {
		t.Fatalf("first error: %s", firstText(first))
	}
	second, err := f.server.handleOrchestratorComposePlan(f.callerCtx(), newCallToolReq("orchestrator_compose_plan", args))
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if second.IsError {
		t.Fatalf("second error: %s", firstText(second))
	}
	text := firstText(second)
	if !strings.Contains(text, `"idempotent":true`) {
		t.Fatalf("second response should be idempotent: %s", text)
	}

	planRows, err := f.server.ledger.List(f.ctx, f.projectID, ledger.ListOptions{
		Type: ledger.TypePlan,
		Tags: []string{"kind:plan"},
	}, nil)
	if err != nil {
		t.Fatalf("list plan rows: %v", err)
	}
	if len(planRows) != 1 {
		t.Fatalf("kind:plan rows after idempotent re-invoke: got %d want 1", len(planRows))
	}

	tasks, err := f.taskStore.List(f.ctx, task.ListOptions{Origin: task.OriginStoryStage}, []string{f.wsID})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	// 4 from the first call, none from the idempotent second call.
	if len(tasks) != 4 {
		t.Fatalf("tasks total after idempotent re-invoke: got %d want 4", len(tasks))
	}
}

// TestOrchestratorComposePlan_AgentOverrides covers the agent_overrides
// path: caller pins a specific agent for one slot; the picker's
// default lookup is bypassed for that slot.
func TestOrchestratorComposePlan_AgentOverrides(t *testing.T) {
	t.Parallel()
	f := newOrchestratorFixture(t)

	// Seed an extra system agent for the override.
	customAgent, err := f.server.docs.Create(context.Background(), document.Document{
		Type:   document.TypeAgent,
		Scope:  document.ScopeSystem,
		Name:   "custom_developer",
		Body:   "custom developer",
		Status: document.StatusActive,
	}, f.now)
	if err != nil {
		t.Fatalf("seed custom agent: %v", err)
	}

	overrideJSON, _ := json.Marshal(map[string]string{"develop": customAgent.ID})
	res, err := f.server.handleOrchestratorComposePlan(f.callerCtx(), newCallToolReq("orchestrator_compose_plan", map[string]any{
		"story_id":        f.storyID,
		"agent_overrides": string(overrideJSON),
	}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if res.IsError {
		t.Fatalf("handler error: %s", firstText(res))
	}
	var body struct {
		AgentAssignments map[string]string `json:"agent_assignments"`
	}
	if err := json.Unmarshal([]byte(firstText(res)), &body); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if body.AgentAssignments["develop"] != customAgent.ID {
		t.Fatalf("override not honored: got %q want %q", body.AgentAssignments["develop"], customAgent.ID)
	}
}
