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
	// resolve to the same row. story_reviewer + development_reviewer
	// are required by sty_c6d76a5b's paired-task emission so the
	// review task's AgentID can be stamped at compose time.
	for _, name := range []string{
		"developer_agent", "releaser_agent", "story_close_agent",
		"story_reviewer", "development_reviewer",
	} {
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

	// Proposed list = the default 5-slot list after preplan removal
	// (epic:v4-lifecycle-refactor, sty_48fdaf8d).
	wantNames := []string{"plan", "develop", "push", "merge_to_main", "story_close"}
	if len(body.ProposedContracts) != len(wantNames) {
		t.Fatalf("proposed_contracts len: got %d want %d (%v)", len(body.ProposedContracts), len(wantNames), body.ProposedContracts)
	}
	for i, name := range wantNames {
		if body.ProposedContracts[i] != name {
			t.Fatalf("proposed[%d]: got %q want %q", i, body.ProposedContracts[i], name)
		}
	}

	// AC3 — paired implement+review tasks enqueued per CI (sty_c6d76a5b).
	// Default 5-slot workflow yields 10 task ids in compose order:
	// implement[0], review[0], implement[1], review[1], …
	if len(body.TaskIDs) != 2*len(wantNames) {
		t.Fatalf("task_ids len: got %d want %d (paired)", len(body.TaskIDs), 2*len(wantNames))
	}
	for slot, name := range wantNames {
		implementID := body.TaskIDs[2*slot]
		reviewID := body.TaskIDs[2*slot+1]

		implTask, gerr := f.taskStore.GetByID(context.Background(), implementID, []string{f.wsID})
		if gerr != nil {
			t.Fatalf("implement task[%d] %s: %v", slot, implementID, gerr)
		}
		if implTask.Kind != task.KindWork {
			t.Errorf("implement task[%d] kind: got %q want %q", slot, implTask.Kind, task.KindWork)
		}
		if implTask.Origin != task.OriginStoryStage {
			t.Errorf("implement task[%d] origin: got %q want %q", slot, implTask.Origin, task.OriginStoryStage)
		}
		if implTask.Status != task.StatusEnqueued {
			t.Errorf("implement task[%d] status: got %q want %q", slot, implTask.Status, task.StatusEnqueued)
		}
		if implTask.AgentID == "" {
			t.Errorf("implement task[%d] agent_id empty; expected per-CI ephemeral", slot)
		}
		if implTask.ContractInstanceID == "" {
			t.Errorf("implement task[%d] contract_instance_id empty", slot)
		}
		// sty_c6d76a5b: tasks are thin — no Payload. Action carries
		// the canonical `contract:<name>` reference; sequence is
		// implicit in the response order.
		if len(implTask.Payload) != 0 {
			t.Errorf("implement task[%d] payload: got %d bytes, want 0 (tasks are thin)", slot, len(implTask.Payload))
		}
		wantAction := task.ContractAction(name)
		if implTask.Action != wantAction {
			t.Errorf("implement task[%d] action: got %q want %q", slot, implTask.Action, wantAction)
		}
		if implTask.StoryID != f.storyID {
			t.Errorf("implement task[%d] story_id: got %q want %q", slot, implTask.StoryID, f.storyID)
		}
		if implTask.Description == "" {
			t.Errorf("implement task[%d] description empty; expected human-readable summary", slot)
		}

		reviewTask, gerr := f.taskStore.GetByID(context.Background(), reviewID, []string{f.wsID})
		if gerr != nil {
			t.Fatalf("review task[%d] %s: %v", slot, reviewID, gerr)
		}
		if reviewTask.Kind != task.KindReview {
			t.Errorf("review task[%d] kind: got %q want %q", slot, reviewTask.Kind, task.KindReview)
		}
		if reviewTask.Status != task.StatusPlanned {
			t.Errorf("review task[%d] status: got %q want %q (must wait for implement close)", slot, reviewTask.Status, task.StatusPlanned)
		}
		if reviewTask.ParentTaskID != implTask.ID {
			t.Errorf("review task[%d] parent_task_id: got %q want %q", slot, reviewTask.ParentTaskID, implTask.ID)
		}
		if reviewTask.AgentID == "" {
			t.Errorf("review task[%d] agent_id empty; expected reviewer agent doc id", slot)
		}
		if reviewTask.AgentID == implTask.AgentID {
			t.Errorf("review task[%d] agent_id %q should not equal implement agent_id (reviewer is persistent, implement is ephemeral)", slot, reviewTask.AgentID)
		}
		if reviewTask.ContractInstanceID != implTask.ContractInstanceID {
			t.Errorf("review task[%d] contract_instance_id: got %q want %q", slot, reviewTask.ContractInstanceID, implTask.ContractInstanceID)
		}
		if len(reviewTask.Payload) != 0 {
			t.Errorf("review task[%d] payload: got %d bytes, want 0 (tasks are thin)", slot, len(reviewTask.Payload))
		}
		if reviewTask.Action != wantAction {
			t.Errorf("review task[%d] action: got %q want %q", slot, reviewTask.Action, wantAction)
		}
		if reviewTask.StoryID != f.storyID {
			t.Errorf("review task[%d] story_id: got %q want %q", slot, reviewTask.StoryID, f.storyID)
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
	// 10 from the first call (default 5-slot workflow paired
	// implement+review per sty_c6d76a5b), none from the idempotent
	// second call.
	if len(tasks) != 10 {
		t.Fatalf("tasks total after idempotent re-invoke: got %d want 10", len(tasks))
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

// TestComposePlan_MintsAgentPerCI (sty_e8d49554 AC2 + AC3): each
// composed CI's AgentID points at a freshly-created project-scope
// ephemeral agent doc carrying ephemeral=true, story_id, and tags
// [ephemeral, story:<id>, ci-bound].
func TestComposePlan_MintsAgentPerCI(t *testing.T) {
	t.Parallel()
	f := newOrchestratorFixture(t)

	res, err := f.server.handleOrchestratorComposePlan(f.callerCtx(), newCallToolReq("orchestrator_compose_plan", map[string]any{
		"story_id": f.storyID,
	}))
	if err != nil || res.IsError {
		t.Fatalf("compose: err=%v body=%s", err, firstText(res))
	}
	var body struct {
		ContractInstances []contract.ContractInstance `json:"contract_instances"`
	}
	if err := json.Unmarshal([]byte(firstText(res)), &body); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(body.ContractInstances) == 0 {
		t.Fatal("no CIs returned")
	}
	for _, ci := range body.ContractInstances {
		if ci.AgentID == "" {
			t.Fatalf("CI %s (%s) AgentID empty — minting did not stamp", ci.ID, ci.ContractName)
		}
		doc, derr := f.server.docs.GetByID(f.ctx, ci.AgentID, nil)
		if derr != nil {
			t.Fatalf("CI %s referenced agent_id %s not found: %v", ci.ID, ci.AgentID, derr)
		}
		if doc.Type != document.TypeAgent {
			t.Errorf("CI %s agent doc type = %q, want agent", ci.ID, doc.Type)
		}
		if doc.Scope != document.ScopeProject {
			t.Errorf("CI %s agent doc scope = %q, want project", ci.ID, doc.Scope)
		}
		if doc.ProjectID == nil || *doc.ProjectID != f.projectID {
			t.Errorf("CI %s agent doc ProjectID = %v, want %q", ci.ID, doc.ProjectID, f.projectID)
		}
		settings, perr := document.UnmarshalAgentSettings(doc.Structured)
		if perr != nil {
			t.Fatalf("CI %s agent settings parse: %v", ci.ID, perr)
		}
		if !settings.Ephemeral {
			t.Errorf("CI %s agent doc Ephemeral = false, want true", ci.ID)
		}
		if settings.StoryID == nil || *settings.StoryID != f.storyID {
			t.Errorf("CI %s agent doc StoryID = %v, want %q", ci.ID, settings.StoryID, f.storyID)
		}
		// AC3 — required tags present.
		want := map[string]bool{"ephemeral": true, "story:" + f.storyID: true, "ci-bound": true}
		for _, tag := range doc.Tags {
			delete(want, tag)
		}
		if len(want) > 0 {
			missing := make([]string, 0, len(want))
			for k := range want {
				missing = append(missing, k)
			}
			t.Errorf("CI %s agent doc missing tags %v; got %v", ci.ID, missing, doc.Tags)
		}
	}
}

// TestMintTaskAgent_PermissionPatternsByCategory (sty_e8d49554 AC4):
// minted agent for plan/develop carries the developer pattern set;
// push/merge_to_main carries releaser; story_close carries closer.
func TestMintTaskAgent_PermissionPatternsByCategory(t *testing.T) {
	t.Parallel()
	f := newOrchestratorFixture(t)

	res, err := f.server.handleOrchestratorComposePlan(f.callerCtx(), newCallToolReq("orchestrator_compose_plan", map[string]any{
		"story_id": f.storyID,
	}))
	if err != nil || res.IsError {
		t.Fatalf("compose: err=%v body=%s", err, firstText(res))
	}
	var body struct {
		ContractInstances []contract.ContractInstance `json:"contract_instances"`
	}
	if err := json.Unmarshal([]byte(firstText(res)), &body); err != nil {
		t.Fatalf("parse: %v", err)
	}

	for _, ci := range body.ContractInstances {
		doc, derr := f.server.docs.GetByID(f.ctx, ci.AgentID, nil)
		if derr != nil {
			t.Fatalf("CI %s agent doc lookup: %v", ci.ID, derr)
		}
		settings, _ := document.UnmarshalAgentSettings(doc.Structured)
		want := defaultPermissionPatterns(ci.ContractName)
		if len(settings.PermissionPatterns) != len(want) {
			t.Errorf("CI %s (%s) pattern count = %d, want %d",
				ci.ID, ci.ContractName, len(settings.PermissionPatterns), len(want))
			continue
		}
		for i, p := range want {
			if settings.PermissionPatterns[i] != p {
				t.Errorf("CI %s (%s) pattern[%d] = %q, want %q",
					ci.ID, ci.ContractName, i, settings.PermissionPatterns[i], p)
			}
		}
	}
}

// TestComposePlan_AgentOverridesHonoured (sty_e8d49554 AC5): when an
// override id is supplied for a contract, no minting happens for it
// — the override id is stamped as-is and no fresh agent doc is
// created for that contract slot.
func TestComposePlan_AgentOverridesHonoured(t *testing.T) {
	t.Parallel()
	f := newOrchestratorFixture(t)

	// Pre-existing agent the caller pins for the plan slot.
	existing, err := f.server.docs.Create(f.ctx, document.Document{
		Type:   document.TypeAgent,
		Scope:  document.ScopeSystem,
		Name:   "preexisting_for_override",
		Body:   "test",
		Status: document.StatusActive,
	}, f.now)
	if err != nil {
		t.Fatalf("seed override agent: %v", err)
	}

	preCount := len(listAgentDocs(t, f))
	overrideJSON, _ := json.Marshal(map[string]string{"plan": existing.ID})

	res, err := f.server.handleOrchestratorComposePlan(f.callerCtx(), newCallToolReq("orchestrator_compose_plan", map[string]any{
		"story_id":        f.storyID,
		"agent_overrides": string(overrideJSON),
	}))
	if err != nil || res.IsError {
		t.Fatalf("compose: err=%v body=%s", err, firstText(res))
	}
	var body struct {
		ContractInstances []contract.ContractInstance `json:"contract_instances"`
		AgentAssignments  map[string]string           `json:"agent_assignments"`
	}
	if err := json.Unmarshal([]byte(firstText(res)), &body); err != nil {
		t.Fatalf("parse: %v", err)
	}

	// Plan CI carries the pinned id.
	if body.AgentAssignments["plan"] != existing.ID {
		t.Errorf("plan assignment = %q, want override %q", body.AgentAssignments["plan"], existing.ID)
	}
	for _, ci := range body.ContractInstances {
		if ci.ContractName == "plan" && ci.AgentID != existing.ID {
			t.Errorf("plan CI AgentID = %q, want override %q", ci.AgentID, existing.ID)
		}
	}

	// Total agent count after compose: 4 minted (develop, push,
	// merge_to_main, story_close — plan was overridden) on top of
	// the pre-existing count.
	postCount := len(listAgentDocs(t, f))
	wantDelta := 4
	if got := postCount - preCount; got != wantDelta {
		t.Errorf("agent doc count delta = %d, want %d (override should skip minting one slot)", got, wantDelta)
	}
}

// TestComposePlan_NoReviewerAgentCreated (sty_e8d49554 AC6): the
// minting flow does NOT create a reviewer agent. The reviewer lives
// on the embedded reviewer service as a persistent agent — not
// per-task.
func TestComposePlan_NoReviewerAgentCreated(t *testing.T) {
	t.Parallel()
	f := newOrchestratorFixture(t)

	res, err := f.server.handleOrchestratorComposePlan(f.callerCtx(), newCallToolReq("orchestrator_compose_plan", map[string]any{
		"story_id": f.storyID,
	}))
	if err != nil || res.IsError {
		t.Fatalf("compose: err=%v body=%s", err, firstText(res))
	}
	for _, doc := range listAgentDocs(t, f) {
		if strings.Contains(strings.ToLower(doc.Name), "reviewer") {
			settings, _ := document.UnmarshalAgentSettings(doc.Structured)
			if settings.Ephemeral {
				t.Errorf("ephemeral reviewer agent created: %q (id=%s) — reviewer must stay persistent", doc.Name, doc.ID)
			}
		}
	}
}

// TestComposePlan_IdempotentReturnsExistingAgents (sty_e8d49554 AC7):
// re-composing a story with existing CIs returns the original CIs
// (and their stamped AgentIDs) without minting fresh agent docs.
func TestComposePlan_IdempotentReturnsExistingAgents(t *testing.T) {
	t.Parallel()
	f := newOrchestratorFixture(t)

	first, err := f.server.handleOrchestratorComposePlan(f.callerCtx(), newCallToolReq("orchestrator_compose_plan", map[string]any{
		"story_id": f.storyID,
	}))
	if err != nil || first.IsError {
		t.Fatalf("first compose: err=%v body=%s", err, firstText(first))
	}
	var firstBody struct {
		ContractInstances []contract.ContractInstance `json:"contract_instances"`
	}
	if err := json.Unmarshal([]byte(firstText(first)), &firstBody); err != nil {
		t.Fatalf("parse first: %v", err)
	}
	firstAgentIDs := make(map[string]string, len(firstBody.ContractInstances))
	for _, ci := range firstBody.ContractInstances {
		firstAgentIDs[ci.ContractName] = ci.AgentID
	}
	agentCountAfterFirst := len(listAgentDocs(t, f))

	second, err := f.server.handleOrchestratorComposePlan(f.callerCtx(), newCallToolReq("orchestrator_compose_plan", map[string]any{
		"story_id": f.storyID,
	}))
	if err != nil || second.IsError {
		t.Fatalf("second compose: err=%v body=%s", err, firstText(second))
	}
	var secondBody struct {
		ContractInstances []contract.ContractInstance `json:"contract_instances"`
		Idempotent        bool                        `json:"idempotent"`
	}
	if err := json.Unmarshal([]byte(firstText(second)), &secondBody); err != nil {
		t.Fatalf("parse second: %v", err)
	}
	if !secondBody.Idempotent {
		t.Errorf("second response should be idempotent")
	}
	for _, ci := range secondBody.ContractInstances {
		if got := ci.AgentID; got != firstAgentIDs[ci.ContractName] {
			t.Errorf("CI %s (%s) AgentID changed: first=%q second=%q",
				ci.ID, ci.ContractName, firstAgentIDs[ci.ContractName], got)
		}
	}
	if got := len(listAgentDocs(t, f)); got != agentCountAfterFirst {
		t.Errorf("agent doc count changed across idempotent re-compose: %d → %d", agentCountAfterFirst, got)
	}
}

// listAgentDocs returns all agent docs visible to the test fixture,
// across system + project scopes. Helper for the new minting tests.
func listAgentDocs(t *testing.T, f *orchestratorFixture) []document.Document {
	t.Helper()
	all, err := f.server.docs.List(f.ctx, document.ListOptions{Type: document.TypeAgent}, nil)
	if err != nil {
		t.Fatalf("list agent docs: %v", err)
	}
	return all
}
