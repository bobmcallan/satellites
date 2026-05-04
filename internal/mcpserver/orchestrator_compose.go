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
	"github.com/bobmcallan/satellites/internal/story"
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

	// Default contract sequence after story_af79cf95 removed the
	// substrate slot algebra. The orchestrator agent body documents
	// the floor (plan + close per pr_mandate_reviewer_enforced);
	// callers may pass a richer plan by way of agent_overrides /
	// future verb args. For the legacy single-shot orchestrator_compose
	// path we keep the default to preserve callers that don't go
	// through orchestrator_submit_plan.
	proposed := []string{"plan", "develop", "push", "merge_to_main", "story_close"}

	now := s.nowUTC()

	// Per-CI ephemeral agent minting (sty_e8d49554). Replaces the
	// hardcoded contract→system-agent map. For each contract slot,
	// either honour an explicit override (skips minting) or mint a
	// project-scope ephemeral agent doc carrying permission patterns
	// derived from the contract category. The minted agent's id is
	// stamped onto the CI via s.contracts.SetAgent after
	// handleWorkflowClaim creates the CIs.
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
		mintedID, mintErr := s.mintTaskAgentForContract(ctx, st, name, now)
		if mintErr != nil {
			return mcpgo.NewToolResultError(fmt.Sprintf("mint agent [%s]: %v", name, mintErr)), nil
		}
		assignments[name] = mintedID
	}

	// kind:plan row — written BEFORE workflow_claim so the audit chain
	// reads plan → workflow-claim → CIs in order.
	planPayload, _ := json.Marshal(map[string]any{
		"proposed_contracts": proposed,
		"agent_assignments":  assignments,
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
		Message           string                      `json:"message,omitempty"`
		_                 map[string]any
	}
	if texts := claimRes.Content; len(texts) > 0 {
		if tc, ok := mcpgo.AsTextContent(texts[0]); ok {
			_ = json.Unmarshal([]byte(tc.Text), &claimBody)
		}
	}

	// Stamp the minted/overridden agent_id onto each CI (sty_e8d49554).
	// handleWorkflowClaim creates CIs without AgentID; the orchestrator
	// owns the assignment and writes it here. Index by contract_name —
	// the assignments map is keyed the same way the workflow_claim
	// produces CIs (one CI per contract_name in proposed order).
	stampedCIs := make([]contract.ContractInstance, 0, len(claimBody.ContractInstances))
	for _, ci := range claimBody.ContractInstances {
		agentID, ok := assignments[ci.ContractName]
		if !ok || agentID == "" {
			stampedCIs = append(stampedCIs, ci)
			continue
		}
		updated, serr := s.contracts.SetAgent(ctx, ci.ID, agentID, now, memberships)
		if serr != nil {
			return mcpgo.NewToolResultError(fmt.Sprintf("stamp agent on CI [%s]: %v", ci.ID, serr)), nil
		}
		stampedCIs = append(stampedCIs, updated)
	}

	body, _ := json.Marshal(map[string]any{
		"story_id":                 storyID,
		"plan_ledger_id":           planRow.ID,
		"workflow_claim_ledger_id": claimBody.ClaimLedgerID,
		"task_ids":                 taskIDs,
		"proposed_contracts":       proposed,
		"agent_assignments":        assignments,
		"contract_instances":       stampedCIs,
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

// defaultPermissionPatterns returns the permission_patterns slice an
// ephemeral agent should carry for a given contract category. Derived
// from the seeded developer_agent / releaser_agent / story_close_agent
// patterns under config/seed/agents/ — kept in sync there. Internal
// default; orchestrator may override per-CI via agent_overrides.
// sty_e8d49554.
func defaultPermissionPatterns(contractName string) []string {
	switch contractName {
	case "plan", "develop":
		return []string{
			"Read:**",
			"Edit:**",
			"Write:**",
			"MultiEdit:**",
			"Grep:**",
			"Glob:**",
			"Bash:git_status",
			"Bash:git_log",
			"Bash:git_diff",
			"Bash:git_show",
			"Bash:git_add",
			"Bash:git_commit",
			"Bash:go_build",
			"Bash:go_test",
			"Bash:go_vet",
			"Bash:go_mod",
			"Bash:go_run",
			"Bash:gofmt",
			"Bash:goimports",
			"Bash:golangci_lint",
			"Bash:ls",
			"Bash:pwd",
			"Bash:cat",
			"Bash:echo",
			"Bash:mkdir",
			"mcp__satellites__satellites_*",
			"mcp__jcodemunch__*",
		}
	case "push", "merge_to_main":
		return []string{
			"Read:**",
			"Bash:git_status",
			"Bash:git_log",
			"Bash:git_diff",
			"Bash:git_fetch",
			"Bash:git_push",
			"Bash:git_checkout",
			"Bash:git_branch",
			"Bash:git_merge",
			"Bash:ls",
			"Bash:pwd",
			"mcp__satellites__satellites_*",
		}
	case "story_close":
		return []string{
			"Read:**",
			"mcp__satellites__satellites_*",
		}
	default:
		// Unknown contract category — return an empty slice. Caller
		// can override via agent_overrides; otherwise the minted agent
		// has no permissions and any tool call will fail closed.
		return nil
	}
}

// mintTaskAgentForContract creates a project-scope ephemeral agent
// document scoped to the story+contract, carrying permission patterns
// derived from the contract category. Returns the new document id.
// sty_e8d49554 — replaces the hardcoded agentRoleForContract lookup
// with on-demand minting so the dispatch surface is data-derived
// (per pr_mandate_configuration_over_code).
func (s *Server) mintTaskAgentForContract(ctx context.Context, st story.Story, contractName string, now time.Time) (string, error) {
	if s.docs == nil {
		return "", fmt.Errorf("document store not configured")
	}
	patterns := defaultPermissionPatterns(contractName)
	settings, err := document.MarshalAgentSettings(document.AgentSettings{
		PermissionPatterns: patterns,
		Ephemeral:          true,
		StoryID:            ledger.StringPtr(st.ID),
	})
	if err != nil {
		return "", fmt.Errorf("marshal agent settings: %w", err)
	}
	projectIDPtr := st.ProjectID
	doc, err := s.docs.Create(ctx, document.Document{
		Type:        document.TypeAgent,
		Scope:       document.ScopeProject,
		ProjectID:   &projectIDPtr,
		WorkspaceID: st.WorkspaceID,
		Name:        fmt.Sprintf("agent_%s_%s", contractName, st.ID),
		Body:        fmt.Sprintf("Ephemeral agent for %s on story %s. Minted at compose time; archived by sweeper after story terminal.", contractName, st.ID),
		Status:      document.StatusActive,
		Structured:  settings,
		Tags:        []string{"ephemeral", "story:" + st.ID, "ci-bound"},
	}, now)
	if err != nil {
		return "", fmt.Errorf("create agent doc: %w", err)
	}
	return doc.ID, nil
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
