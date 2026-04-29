// Package mcpserver — orchestrator_compose_plan handler. Story_66d4249f
// (S6 of epic:orchestrator-driven-configuration). Wires the
// orchestrator role at the story-implement entry path: reads the
// resolved scope mandate stack, picks an agent per slot, writes a
// kind:plan ledger row, enqueues per-slot tasks, and calls
// workflow_claim. The plan IS the configuration.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/task"
)

// orchestratorTaskPayload is the JSON payload encoded onto each
// per-slot task. Carries the contract_name, the resolved agent_ref
// (empty when no matching agent was found), and the slot sequence.
type orchestratorTaskPayload struct {
	StoryID      string `json:"story_id"`
	ContractName string `json:"contract_name"`
	AgentRef     string `json:"agent_ref,omitempty"`
	Sequence     int    `json:"sequence"`
}

// handleOrchestratorComposePlan implements `orchestrator_compose_plan`.
// Required arg: story_id. Optional: agent_overrides (JSON object mapping
// contract_name -> agent_ref) when the caller wants to pin a non-default
// agent for a specific slot.
//
// On success, returns:
//
//	{
//	  story_id, plan_ledger_id, workflow_claim_ledger_id,
//	  task_ids: [...], proposed_contracts: [...],
//	  agent_assignments: { contract_name: agent_ref },
//	  contract_instances: [...]
//	}
//
// Idempotent: when CIs already exist on the story, returns the existing
// CI list without enqueuing duplicate tasks or writing a fresh plan
// row. Mirrors workflow_claim's idempotence convention.
func (s *Server) handleOrchestratorComposePlan(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	start := time.Now()
	caller, _ := UserFrom(ctx)

	storyID, err := req.RequireString("story_id")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}

	if s.stories == nil || s.contracts == nil || s.docs == nil || s.ledger == nil {
		return mcpgo.NewToolResultError("orchestrator_compose_plan unavailable: required stores not configured"), nil
	}

	memberships := s.resolveCallerMemberships(ctx, caller)
	st, err := s.stories.GetByID(ctx, storyID, memberships)
	if err != nil {
		return mcpgo.NewToolResultError("story not found"), nil
	}

	// Idempotence — if the story already has CIs, return them without
	// duplicating the plan/tasks. Same convention workflow_claim follows.
	if existing, _ := s.contracts.List(ctx, storyID, memberships); len(existing) > 0 {
		body, _ := json.Marshal(map[string]any{
			"story_id":           storyID,
			"contract_instances": existing,
			"idempotent":         true,
		})
		return mcpgo.NewToolResultText(string(body)), nil
	}

	spec, err := s.loadResolvedWorkflowSpec(ctx, st.WorkspaceID, st.ProjectID, caller.UserID, memberships)
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	proposed := expandResolvedDefault(spec)
	if len(proposed) == 0 {
		return mcpgo.NewToolResultError("orchestrator_compose_plan: no required slots in resolved scope mandate stack"), nil
	}

	overrides := parseAgentOverrides(req.GetString("agent_overrides", ""))
	assignments := make(map[string]string, len(proposed))
	uniqueByContract := make(map[string]struct{}, len(proposed))
	for _, name := range proposed {
		if _, seen := uniqueByContract[name]; seen {
			continue
		}
		uniqueByContract[name] = struct{}{}
		if pinned, ok := overrides[name]; ok && pinned != "" {
			assignments[name] = pinned
			continue
		}
		assignments[name] = s.pickAgentForContract(ctx, name)
	}

	now := s.nowUTC()

	// kind:plan row — written BEFORE workflow_claim so the audit chain
	// reads plan → workflow-claim → CIs in order.
	planPayload, _ := json.Marshal(map[string]any{
		"proposed_contracts": proposed,
		"agent_assignments":  assignments,
		"resolved_spec":      spec,
	})
	planRow, err := s.ledger.Append(ctx, ledger.LedgerEntry{
		WorkspaceID: st.WorkspaceID,
		ProjectID:   st.ProjectID,
		StoryID:     ledger.StringPtr(storyID),
		Type:        ledger.TypePlan,
		Tags:        []string{"kind:plan", "phase:orchestrator", "story:" + storyID},
		Content:     fmt.Sprintf("orchestrator plan: %s", strings.Join(proposed, " → ")),
		Structured:  planPayload,
		CreatedBy:   caller.UserID,
	}, now)
	if err != nil {
		return mcpgo.NewToolResultError(fmt.Sprintf("plan ledger append: %v", err)), nil
	}

	// Enqueue one task per slot — origin=story_stage, payload encodes
	// {contract_name, agent_ref, sequence}. The task carries the slot
	// metadata so a worker (or the dispatcher) can later claim it.
	taskIDs := make([]string, 0, len(proposed))
	if s.tasks != nil {
		for i, name := range proposed {
			payload, _ := json.Marshal(orchestratorTaskPayload{
				StoryID:      storyID,
				ContractName: name,
				AgentRef:     assignments[name],
				Sequence:     i,
			})
			t, terr := s.tasks.Enqueue(ctx, task.Task{
				WorkspaceID: st.WorkspaceID,
				ProjectID:   st.ProjectID,
				Origin:      task.OriginStoryStage,
				Priority:    task.PriorityMedium,
				Payload:     payload,
			}, now)
			if terr != nil {
				return mcpgo.NewToolResultError(fmt.Sprintf("task enqueue [%d %s]: %v", i, name, terr)), nil
			}
			taskIDs = append(taskIDs, t.ID)
		}
	}

	// Plan-approval precondition (story_a5826137): handleWorkflowClaim
	// requires a kind:plan-approved ledger row scoped to the story. The
	// orchestrator-compose path is the legacy single-shot entry point —
	// it writes the row inline so its existing callers continue to
	// work. The new entry point (satellites_orchestrator_submit_plan)
	// is preferred for new stories because it runs the
	// reviewer-approval loop instead of auto-approving.
	planApprovedPayload, _ := json.Marshal(map[string]any{
		"iteration":          1,
		"proposed_contracts": proposed,
		"source":             "orchestrator_compose_plan",
	})
	if _, err := s.ledger.Append(ctx, ledger.LedgerEntry{
		WorkspaceID: st.WorkspaceID,
		ProjectID:   st.ProjectID,
		StoryID:     ledger.StringPtr(storyID),
		Type:        ledger.TypeDecision,
		Tags:        []string{planApprovedKind, planApprovedPhase},
		Content:     "auto-approved via orchestrator_compose_plan",
		Structured:  planApprovedPayload,
		CreatedBy:   caller.UserID,
	}, now); err != nil {
		return mcpgo.NewToolResultError(fmt.Sprintf("plan-approved ledger append: %v", err)), nil
	}

	// Now claim the workflow — this writes the kind:workflow-claim row
	// + creates the CIs.
	claimMD := fmt.Sprintf("orchestrator-composed plan for %s", storyID)
	claimReq := newOrchestratorClaimReq(storyID, proposed, claimMD)
	claimRes, err := s.handleWorkflowClaim(ctx, claimReq)
	if err != nil {
		return mcpgo.NewToolResultError(fmt.Sprintf("workflow_claim: %v", err)), nil
	}
	if claimRes.IsError {
		return claimRes, nil
	}
	var claimBody struct {
		ClaimLedgerID     string                      `json:"claim_ledger_id"`
		ContractInstances []contract.ContractInstance `json:"contract_instances"`
		Idempotent        bool                        `json:"idempotent,omitempty"`
		Error             string                      `json:"error,omitempty"`
		ContractName      string                      `json:"contract_name,omitempty"`
		Source            string                      `json:"source,omitempty"`
		Message           string                      `json:"message,omitempty"`
		Spec              contract.WorkflowSpec       `json:"spec,omitempty"`
		_                 map[string]any
	}
	if texts := claimRes.Content; len(texts) > 0 {
		if tc, ok := mcpgo.AsTextContent(texts[0]); ok {
			_ = json.Unmarshal([]byte(tc.Text), &claimBody)
		}
	}

	body, _ := json.Marshal(map[string]any{
		"story_id":                 storyID,
		"plan_ledger_id":           planRow.ID,
		"workflow_claim_ledger_id": claimBody.ClaimLedgerID,
		"task_ids":                 taskIDs,
		"proposed_contracts":       proposed,
		"agent_assignments":        assignments,
		"contract_instances":       claimBody.ContractInstances,
	})
	s.logger.Info().
		Str("method", "tools/call").
		Str("tool", "orchestrator_compose_plan").
		Str("story_id", storyID).
		Int("slots", len(proposed)).
		Int("tasks", len(taskIDs)).
		Int64("duration_ms", time.Since(start).Milliseconds()).
		Msg("mcp tool call")
	return mcpgo.NewToolResultText(string(body)), nil
}

// agentRoleForContract maps a contract_name to the system role agent
// that drives it after the S8 collapse (story_87b46d01). Multiple
// contracts intentionally resolve to the same role agent: the
// developer_agent drives preplan/plan/develop; the releaser_agent
// drives push/merge_to_main; the story_close_agent drives
// story_close. Unknown contract names map to "" (handler then falls
// back to the legacy <contract>_agent / agent_<contract> name match
// so project-scope custom contracts still resolve).
var agentRoleForContract = map[string]string{
	"preplan":       "developer_agent",
	"plan":          "developer_agent",
	"develop":       "developer_agent",
	"push":          "releaser_agent",
	"merge_to_main": "releaser_agent",
	"story_close":   "story_close_agent",
}

// pickAgentForContract returns the system agent's id assigned to the
// given contract slot. The role-based mapping (story_87b46d01) is
// consulted first — every lifecycle contract resolves to one of three
// role agents (developer_agent, releaser_agent, story_close_agent).
// When the contract is not one of the lifecycle slots, the picker
// falls back to a name-match against `<contract_name>_agent` /
// `agent_<contract_name>` so project-scope custom contracts still
// have a deterministic default agent.
func (s *Server) pickAgentForContract(ctx context.Context, contractName string) string {
	if s.docs == nil {
		return ""
	}
	candidates, err := s.docs.List(ctx, document.ListOptions{
		Type:  document.TypeAgent,
		Scope: document.ScopeSystem,
	}, nil)
	if err != nil {
		return ""
	}
	if roleName, ok := agentRoleForContract[contractName]; ok {
		for _, d := range candidates {
			if d.Status != document.StatusActive {
				continue
			}
			if d.Name == roleName {
				return d.ID
			}
		}
	}
	wantA := contractName + "_agent"
	wantB := "agent_" + contractName
	for _, d := range candidates {
		if d.Status != document.StatusActive {
			continue
		}
		if d.Name == wantA || d.Name == wantB {
			return d.ID
		}
	}
	return ""
}

// parseAgentOverrides decodes the optional agent_overrides argument as
// a JSON object {contract_name: agent_ref}. Empty/invalid input
// returns an empty map.
func parseAgentOverrides(raw string) map[string]string {
	if raw == "" {
		return map[string]string{}
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return map[string]string{}
	}
	return out
}

// newOrchestratorClaimReq builds the minimal CallToolRequest the
// existing handleWorkflowClaim expects. Inlined here so production code
// does not depend on the *_test.go newCallToolReq helper.
func newOrchestratorClaimReq(storyID string, proposed []string, claimMD string) mcpgo.CallToolRequest {
	req := mcpgo.CallToolRequest{}
	req.Params.Name = "workflow_claim"
	req.Params.Arguments = map[string]any{
		"story_id":           storyID,
		"proposed_contracts": proposed,
		"claim_markdown":     claimMD,
	}
	return req
}
