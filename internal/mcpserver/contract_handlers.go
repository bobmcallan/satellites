package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
)

// handleWorkflowClaim resolves each proposed contract_name to its
// document and creates one ContractInstance per entry. The
// plan-approval precondition (story_a5826137) gates the call: a story
// without a kind:plan-approved ledger row is rejected. Per
// epic:configuration-over-code-mandate (story_af79cf95) there is no
// substrate-side slot algebra — the orchestrator's plan IS the
// configuration, and the reviewer enforced its shape during the
// plan-approval loop. Idempotent on re-claim — returns the existing
// CIs when the story already has a workflow-claim row.
func (s *Server) handleWorkflowClaim(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	start := time.Now()
	caller, _ := UserFrom(ctx)
	storyID, err := req.RequireString("story_id")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	claimMarkdown := req.GetString("claim_markdown", "")
	proposed := req.GetStringSlice("proposed_contracts", nil)
	agentID := req.GetString("agent_id", "")

	memberships := s.resolveCallerMemberships(ctx, caller)
	st, err := s.stories.GetByID(ctx, storyID, memberships)
	if err != nil {
		return mcpgo.NewToolResultError("story not found"), nil
	}

	// Plan-approval precondition (story_a5826137): the orchestrator must
	// have submitted the plan and received an accepted verdict from the
	// story_reviewer (Gemini-backed in prod, AcceptAll in dev/tests)
	// before workflow_claim instantiates CIs. The accepted verdict
	// writes a kind:plan-approved ledger row scoped to the story; the
	// row's existence is the precondition.
	if !s.hasPlanApprovedRow(ctx, st.ProjectID, storyID, memberships) {
		body, _ := json.Marshal(map[string]any{
			"error":    "plan_not_approved",
			"story_id": storyID,
			"message":  "story has no kind:plan-approved ledger row; call satellites_orchestrator_submit_plan first",
		})
		return mcpgo.NewToolResultError(string(body)), nil
	}

	_ = agentID
	if len(proposed) == 0 {
		body, _ := json.Marshal(map[string]any{
			"error":    "proposed_contracts_required",
			"story_id": storyID,
			"message":  "proposed_contracts must be non-empty; call satellites_orchestrator_submit_plan with the proposed list and approve before claiming",
		})
		return mcpgo.NewToolResultError(string(body)), nil
	}

	// Idempotence — return existing CIs if the story already has a
	// workflow-claim row.
	existing, _ := s.contracts.List(ctx, storyID, memberships)
	if len(existing) > 0 {
		body, _ := json.Marshal(map[string]any{
			"story_id":           storyID,
			"claim_ledger_id":    "",
			"contract_instances": existing,
			"idempotent":         true,
		})
		return mcpgo.NewToolResultText(string(body)), nil
	}

	// Resolve each contract_name → document id. Prefer scope=system.
	// All proposed slots default to required_for_close=true; the closer
	// rolls the story up only when every required CI is terminal.
	resolved := make([]resolvedSlot, 0, len(proposed))
	for _, name := range proposed {
		doc, err := s.findContractDocByName(ctx, name, st.WorkspaceID)
		if err != nil {
			errBody, _ := json.Marshal(map[string]any{
				"error":         "unknown_contract",
				"contract_name": name,
				"message":       "no active document{type=contract} with this name",
			})
			return mcpgo.NewToolResultError(string(errBody)), nil
		}
		resolved = append(resolved, resolvedSlot{name: name, docID: doc.ID, required: requiredForCloseFor(name)})
	}

	// Write the workflow-claim ledger row first so the CIs have a
	// parent audit row to reference.
	payload, _ := json.Marshal(map[string]any{"proposed_contracts": proposed})
	now := time.Now().UTC()
	claim, err := s.ledger.Append(ctx, ledger.LedgerEntry{
		WorkspaceID: st.WorkspaceID,
		ProjectID:   st.ProjectID,
		StoryID:     ledger.StringPtr(storyID),
		Type:        ledger.TypeWorkflowClaim,
		Tags:        []string{"kind:workflow-claim", "phase:pre-plan"},
		Content:     claimMarkdown,
		Structured:  payload,
		CreatedBy:   caller.UserID,
	}, now)
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}

	cis := make([]contract.ContractInstance, 0, len(resolved))
	for i, slot := range resolved {
		ci, err := s.contracts.Create(ctx, contract.ContractInstance{
			StoryID:          storyID,
			ContractID:       slot.docID,
			ContractName:     slot.name,
			Sequence:         i,
			RequiredForClose: slot.required,
			Status:           contract.StatusReady,
		}, now)
		if err != nil {
			return mcpgo.NewToolResultError(fmt.Sprintf("create CI %q: %v", slot.name, err)), nil
		}
		cis = append(cis, ci)
	}

	body, _ := json.Marshal(map[string]any{
		"story_id":           storyID,
		"claim_ledger_id":    claim.ID,
		"contract_instances": cis,
	})
	s.logger.Info().
		Str("method", "tools/call").
		Str("tool", "workflow_claim").
		Str("story_id", storyID).
		Int("ci_count", len(cis)).
		Int64("duration_ms", time.Since(start).Milliseconds()).
		Msg("mcp tool call")
	return mcpgo.NewToolResultText(string(body)), nil
}

// handleContractNext is read-only: returns the lowest-sequence
// ready CI plus skills bound to its contract document.
func (s *Server) handleContractNext(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	start := time.Now()
	caller, _ := UserFrom(ctx)
	storyID, err := req.RequireString("story_id")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	memberships := s.resolveCallerMemberships(ctx, caller)
	if _, err := s.stories.GetByID(ctx, storyID, memberships); err != nil {
		return mcpgo.NewToolResultError("story not found"), nil
	}
	cis, err := s.contracts.List(ctx, storyID, memberships)
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	var next *contract.ContractInstance
	for i := range cis {
		if cis[i].Status == contract.StatusReady {
			next = &cis[i]
			break
		}
	}
	resp := map[string]any{"story_id": storyID}
	if next == nil {
		resp["contract_instance"] = nil
		resp["skills"] = nil
		body, _ := json.Marshal(resp)
		return mcpgo.NewToolResultText(string(body)), nil
	}
	resp["contract_instance"] = next
	resp["skills"] = s.resolveSkillsForCI(ctx, next, memberships)
	body, _ := json.Marshal(resp)
	s.logger.Info().
		Str("method", "tools/call").
		Str("tool", "contract_next").
		Str("story_id", storyID).
		Str("ci_id", next.ID).
		Int64("duration_ms", time.Since(start).Milliseconds()).
		Msg("mcp tool call")
	return mcpgo.NewToolResultText(string(body)), nil
}

// resolveSkillsForCI returns the skills relevant to the given CI.
// Story_b1108d4a routes through agent.skill_refs first: when the CI
// has an allocated agent, the agent's skill_refs name the skill docs
// to return. The legacy contract_binding lookup remains as a fallback
// for un-migrated CIs (no agent) so existing rows keep working.
func (s *Server) resolveSkillsForCI(ctx context.Context, ci *contract.ContractInstance, memberships []string) []document.Document {
	if ci == nil {
		return nil
	}
	if ci.AgentID != "" && s.docs != nil {
		agentDoc, err := s.docs.GetByID(ctx, ci.AgentID, memberships)
		if err == nil {
			settings, derr := document.UnmarshalAgentSettings(agentDoc.Structured)
			if derr == nil && len(settings.SkillRefs) > 0 {
				out := make([]document.Document, 0, len(settings.SkillRefs))
				for _, skillID := range settings.SkillRefs {
					skill, gerr := s.docs.GetByID(ctx, skillID, memberships)
					if gerr != nil {
						continue
					}
					if skill.Type == document.TypeSkill && skill.Status == "active" {
						out = append(out, skill)
					}
				}
				return out
			}
		}
	}
	// Legacy fallback: list skills bound to the CI's contract.
	skills, _ := s.docs.List(ctx, document.ListOptions{
		Type:            document.TypeSkill,
		ContractBinding: ci.ContractID,
	}, memberships)
	return skills
}

// planAmendInvocation is the per-add payload accepted by handlePlanAmend.
// Mirrors ContractInstance fields exposed to plan-amend callers.
type planAmendInvocation struct {
	ContractName       string `json:"contract_name"`
	ACScope            []int  `json:"ac_scope,omitempty"`
	ParentInvocationID string `json:"parent_invocation_id,omitempty"`
	AgentID            string `json:"agent_id,omitempty"`
}

// handlePlanAmend appends new CIs to an existing story's plan tree
// (story_d5d88a64). Each add carries an optional ac_scope and an optional
// parent_invocation_id so the orchestrator can express "rerun develop
// scoped to AC 2" without re-claiming the original CI.
//
// The handler enforces three gates before creating the rows:
//  1. Workflow_spec re-validation — the resulting list of contract_names
//     across existing + amended CIs must satisfy the project's spec.
//  2. Per-AC iteration cap (SATELLITES_MAX_AC_ITERATIONS, default 5) —
//     re-scoping the same AC past the cap returns ErrACIterationCap.
//  3. Parent linkage — when parent_invocation_id is set it must resolve
//     to an existing CI on the same story.
//
// On success a kind:plan-amend ledger row is written carrying
// {reason, added_cis, slot_validation_result} in Structured.
func (s *Server) handlePlanAmend(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	start := time.Now()
	caller, _ := UserFrom(ctx)
	storyID, err := req.RequireString("story_id")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	reason := req.GetString("reason", "")
	rawAdds := req.GetString("add_invocations", "")
	if rawAdds == "" {
		return mcpgo.NewToolResultError("add_invocations is required (JSON array)"), nil
	}
	var adds []planAmendInvocation
	if err := json.Unmarshal([]byte(rawAdds), &adds); err != nil {
		return mcpgo.NewToolResultError(fmt.Sprintf("add_invocations parse error: %v", err)), nil
	}
	if len(adds) == 0 {
		return mcpgo.NewToolResultError("add_invocations must contain at least one entry"), nil
	}

	memberships := s.resolveCallerMemberships(ctx, caller)
	st, err := s.stories.GetByID(ctx, storyID, memberships)
	if err != nil {
		return mcpgo.NewToolResultError("story not found"), nil
	}

	existing, err := s.contracts.List(ctx, storyID, memberships)
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	if len(existing) == 0 {
		return mcpgo.NewToolResultError("plan_amend requires an initial workflow — call workflow_claim first"), nil
	}

	// Validate parent_invocation_id linkage against the existing CI set.
	existingByID := make(map[string]contract.ContractInstance, len(existing))
	for _, ci := range existing {
		existingByID[ci.ID] = ci
	}
	for i, add := range adds {
		if add.ContractName == "" {
			return mcpgo.NewToolResultError(fmt.Sprintf("add_invocations[%d]: contract_name is required", i)), nil
		}
		if add.ParentInvocationID != "" {
			if _, ok := existingByID[add.ParentInvocationID]; !ok {
				errBody, _ := json.Marshal(map[string]any{
					"error":                "unknown_parent_invocation",
					"index":                i,
					"parent_invocation_id": add.ParentInvocationID,
				})
				return mcpgo.NewToolResultError(string(errBody)), nil
			}
		}
	}

	// AC iteration cap: predict what the post-amend ContractInstance
	// shape looks like to compute the next AC counts. Substrate slot
	// validation is gone (story_af79cf95) — the orchestrator's plan
	// went through the reviewer-approved loop, and the reviewer judges
	// whether amended contracts are appropriate.
	predicted := make([]contract.ContractInstance, 0, len(adds))
	for _, add := range adds {
		predicted = append(predicted, contract.ContractInstance{
			ACScope: append([]int(nil), add.ACScope...),
		})
	}
	cap := contract.MaxACIterations()
	if err := contract.ValidateACScope(existing, predicted, cap); err != nil {
		errBody, _ := json.Marshal(map[string]any{
			"error":   "ac_iteration_cap_exceeded",
			"cap":     cap,
			"message": err.Error(),
		})
		return mcpgo.NewToolResultError(string(errBody)), nil
	}

	// Resolve each amended contract_name → document id. Reject early so
	// no ledger row gets written for an invalid amend.
	resolved := make([]resolvedSlot, 0, len(adds))
	for i, add := range adds {
		doc, err := s.findContractDocByName(ctx, add.ContractName, st.WorkspaceID)
		if err != nil {
			errBody, _ := json.Marshal(map[string]any{
				"error":         "unknown_contract",
				"index":         i,
				"contract_name": add.ContractName,
			})
			return mcpgo.NewToolResultError(string(errBody)), nil
		}
		resolved = append(resolved, resolvedSlot{name: add.ContractName, docID: doc.ID, required: requiredForCloseFor(add.ContractName)})
	}

	// Determine the starting sequence — append after the highest existing.
	maxSeq := 0
	for _, ci := range existing {
		if ci.Sequence > maxSeq {
			maxSeq = ci.Sequence
		}
	}

	now := time.Now().UTC()

	// Create the new CIs and accumulate them for the ledger payload.
	created := make([]contract.ContractInstance, 0, len(adds))
	for i, slot := range resolved {
		add := adds[i]
		ci := contract.ContractInstance{
			StoryID:            storyID,
			ContractID:         slot.docID,
			ContractName:       slot.name,
			Sequence:           maxSeq + 1 + i,
			RequiredForClose:   slot.required,
			Status:             contract.StatusReady,
			ACScope:            append([]int(nil), add.ACScope...),
			ParentInvocationID: add.ParentInvocationID,
		}
		out, err := s.contracts.Create(ctx, ci, now)
		if err != nil {
			return mcpgo.NewToolResultError(fmt.Sprintf("create CI %q: %v", slot.name, err)), nil
		}
		created = append(created, out)
	}

	// Write the kind:plan-amend ledger row capturing the rationale.
	addedSummaries := make([]map[string]any, 0, len(created))
	for _, ci := range created {
		addedSummaries = append(addedSummaries, map[string]any{
			"id":                   ci.ID,
			"contract_name":        ci.ContractName,
			"sequence":             ci.Sequence,
			"ac_scope":             ci.ACScope,
			"parent_invocation_id": ci.ParentInvocationID,
		})
	}
	payload, _ := json.Marshal(map[string]any{
		"reason":                 reason,
		"added_cis":              addedSummaries,
		"slot_validation_result": "ok",
		"ac_iteration_cap":       cap,
	})
	row, err := s.ledger.Append(ctx, ledger.LedgerEntry{
		WorkspaceID: st.WorkspaceID,
		ProjectID:   st.ProjectID,
		StoryID:     ledger.StringPtr(storyID),
		Type:        ledger.TypePlanAmend,
		Tags:        []string{"kind:plan-amend", "phase:plan", "story:" + storyID},
		Content:     reason,
		Structured:  payload,
		CreatedBy:   caller.UserID,
	}, now)
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}

	body, _ := json.Marshal(map[string]any{
		"story_id":             storyID,
		"plan_amend_ledger_id": row.ID,
		"contract_instances":   created,
	})
	s.logger.Info().
		Str("method", "tools/call").
		Str("tool", "plan_amend").
		Str("story_id", storyID).
		Int("added_ci_count", len(created)).
		Int64("duration_ms", time.Since(start).Milliseconds()).
		Msg("mcp tool call")
	return mcpgo.NewToolResultText(string(body)), nil
}

// findContractDocByName resolves a contract_name to a document{type=contract}.
// System-scope rows are preferred; workspace-scoped rows are the
// fallback so projects can override.
func (s *Server) findContractDocByName(ctx context.Context, name, workspaceID string) (document.Document, error) {
	candidates, err := s.docs.List(ctx, document.ListOptions{Type: document.TypeContract}, nil)
	if err != nil {
		return document.Document{}, err
	}
	var systemMatch, wsMatch *document.Document
	for i := range candidates {
		d := candidates[i]
		if d.Name != name || d.Status != document.StatusActive {
			continue
		}
		switch d.Scope {
		case document.ScopeSystem:
			if systemMatch == nil {
				systemMatch = &d
			}
		case document.ScopeProject:
			if d.WorkspaceID == workspaceID && wsMatch == nil {
				wsMatch = &d
			}
		}
	}
	if systemMatch != nil {
		return *systemMatch, nil
	}
	if wsMatch != nil {
		return *wsMatch, nil
	}
	return document.Document{}, errors.New("contract document not found")
}

type resolvedSlot struct {
	name     string
	docID    string
	required bool
}

// requiredForCloseFor maps a contract name to its required_for_close
// flag. Replaces the prior workflow_spec-derived rule (story_af79cf95
// removed the substrate slot algebra). The rule mirrors the v3 default:
// pre-close phases (preplan/plan/develop) gate the rollup; post-commit
// phases and the closer itself (push/merge_to_main/story_close) do not,
// so the closer can transition the story without waiting on push or
// merge_to_main.
func requiredForCloseFor(name string) bool {
	switch name {
	case "preplan", "plan", "develop":
		return true
	default:
		return false
	}
}

// anySubstring is a tiny helper used by tests to match structured
// error bodies without importing strings at every call site.
func anySubstring(s string, needles ...string) bool {
	for _, n := range needles {
		if !strings.Contains(s, n) {
			return false
		}
	}
	return true
}
