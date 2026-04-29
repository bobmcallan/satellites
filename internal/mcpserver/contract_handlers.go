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

// specKeyTag is the tag convention the workflow_spec kv row uses.
// Kept low-cardinality so KVProjection (derivations slice 7.3) can
// collapse versions without a secondary index.
const specKeyTag = "key:workflow_spec"

// handleProjectWorkflowSpecGet loads the project's workflow_spec from
// the latest kv ledger row tagged key:workflow_spec. Falls back to
// DefaultWorkflowSpec when no row exists.
func (s *Server) handleProjectWorkflowSpecGet(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	start := time.Now()
	caller, _ := UserFrom(ctx)
	projectID, err := req.RequireString("project_id")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	memberships := s.resolveCallerMemberships(ctx, caller)
	resolvedID, err := s.resolveProjectID(ctx, projectID, caller, memberships)
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	spec, err := s.loadWorkflowSpec(ctx, resolvedID, memberships)
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	body, _ := json.Marshal(map[string]any{"project_id": resolvedID, "spec": spec})
	s.logger.Info().
		Str("method", "tools/call").
		Str("tool", "project_workflow_spec_get").
		Str("project_id", resolvedID).
		Int("slot_count", len(spec.Slots)).
		Int64("duration_ms", time.Since(start).Milliseconds()).
		Msg("mcp tool call")
	return mcpgo.NewToolResultText(string(body)), nil
}

// handleProjectWorkflowSpecSet persists a WorkflowSpec by appending a
// new kv row. Older rows stay in the audit chain; KVProjection reads
// the latest per key.
func (s *Server) handleProjectWorkflowSpecSet(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	start := time.Now()
	caller, _ := UserFrom(ctx)
	projectID, err := req.RequireString("project_id")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	raw := req.GetString("slots", "")
	if raw == "" {
		return mcpgo.NewToolResultError("slots is required (JSON array)"), nil
	}
	var slots []contract.Slot
	if err := json.Unmarshal([]byte(raw), &slots); err != nil {
		return mcpgo.NewToolResultError(fmt.Sprintf("slots parse error: %v", err)), nil
	}
	if len(slots) == 0 {
		return mcpgo.NewToolResultError("slots must contain at least one entry"), nil
	}
	memberships := s.resolveCallerMemberships(ctx, caller)
	resolvedID, err := s.resolveProjectID(ctx, projectID, caller, memberships)
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	wsID := s.resolveProjectWorkspaceID(ctx, resolvedID)
	structured, _ := json.Marshal(contract.WorkflowSpec{Slots: slots})
	row, err := s.ledger.Append(ctx, ledger.LedgerEntry{
		WorkspaceID: wsID,
		ProjectID:   resolvedID,
		Type:        ledger.TypeKV,
		Tags:        []string{specKeyTag},
		Content:     "workflow_spec",
		Structured:  structured,
		CreatedBy:   caller.UserID,
	}, time.Now().UTC())
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	body, _ := json.Marshal(map[string]any{
		"project_id": resolvedID,
		"ledger_id":  row.ID,
		"spec":       contract.WorkflowSpec{Slots: slots},
	})
	s.logger.Info().
		Str("method", "tools/call").
		Str("tool", "project_workflow_spec_set").
		Str("project_id", resolvedID).
		Int("slot_count", len(slots)).
		Int64("duration_ms", time.Since(start).Milliseconds()).
		Msg("mcp tool call")
	return mcpgo.NewToolResultText(string(body)), nil
}

// handleWorkflowClaim validates proposed against the project's
// spec, resolves each contract_name to its document, and creates one
// ContractInstance per slot. Idempotent on re-claim — returns the
// existing CIs if a kind:workflow-claim row already exists for the
// story.
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

	spec, err := s.loadWorkflowSpec(ctx, st.ProjectID, memberships)
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}

	if len(proposed) == 0 {
		// Resolution precedence (story_4ca6cb1b + story_fb600b97):
		//   1. story.configuration_id (per-story override)
		//   2. agent.default_configuration_id (per-agent default; only
		//      consulted when agent_id is supplied + the agent carries
		//      one)
		//   3. project workflow_spec default
		// Per-call proposed_contracts (handled in the surrounding
		// branch) wins above all of these.
		var cfgID *string
		if st.ConfigurationID != nil && *st.ConfigurationID != "" {
			cfgID = st.ConfigurationID
		} else if agentID != "" {
			agentCfg, err := s.resolveAgentDefaultConfigurationID(ctx, agentID, memberships)
			if err != nil {
				return mcpgo.NewToolResultError(err.Error()), nil
			}
			if agentCfg != "" {
				v := agentCfg
				cfgID = &v
			}
		}
		if cfgProposed, err := s.resolveConfigurationProposed(ctx, cfgID, memberships); err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		} else if len(cfgProposed) > 0 {
			proposed = cfgProposed
		} else {
			proposed = expandDefaultProposed(spec)
		}
	}
	if err := spec.Validate(proposed); err != nil {
		return mcpgo.NewToolResultText(marshalSpecError(err)), nil
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
		resolved = append(resolved, resolvedSlot{name: name, docID: doc.ID, required: specSlotRequired(spec, name)})
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

	spec, err := s.loadWorkflowSpec(ctx, st.ProjectID, memberships)
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
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

	// Build the proposed list (existing names + amended names) and
	// validate against the spec before any writes.
	proposed := make([]string, 0, len(existing)+len(adds))
	for _, ci := range existing {
		proposed = append(proposed, ci.ContractName)
	}
	for _, add := range adds {
		proposed = append(proposed, add.ContractName)
	}
	if err := spec.Validate(proposed); err != nil {
		return mcpgo.NewToolResultText(marshalSpecError(err)), nil
	}

	// AC iteration cap: predict what the post-amend ContractInstance
	// shape looks like to compute the next AC counts.
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
		resolved = append(resolved, resolvedSlot{name: add.ContractName, docID: doc.ID, required: specSlotRequired(spec, add.ContractName)})
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

// loadWorkflowSpec reads the project's latest kv row tagged
// key:workflow_spec and decodes its Structured payload. Falls back to
// DefaultWorkflowSpec when no row exists or decode fails.
func (s *Server) loadWorkflowSpec(ctx context.Context, projectID string, memberships []string) (contract.WorkflowSpec, error) {
	if s.ledger == nil {
		return contract.DefaultWorkflowSpec(), nil
	}
	rows, err := s.ledger.List(ctx, projectID, ledger.ListOptions{
		Type:  ledger.TypeKV,
		Tags:  []string{specKeyTag},
		Limit: 1,
	}, memberships)
	if err != nil {
		return contract.WorkflowSpec{}, fmt.Errorf("spec load: %w", err)
	}
	if len(rows) == 0 {
		return contract.DefaultWorkflowSpec(), nil
	}
	var spec contract.WorkflowSpec
	if err := json.Unmarshal(rows[0].Structured, &spec); err != nil || len(spec.Slots) == 0 {
		return contract.DefaultWorkflowSpec(), nil
	}
	return spec, nil
}

// resolveConfigurationProposed derives the proposed_contracts list from
// a story's referenced Configuration. Returns (nil, nil) when the
// configurationID pointer is nil/empty or the doc store is unavailable
// — caller falls back to the project default. Returns an error when the
// id is set but cannot be resolved or any of its ContractRefs cannot be
// turned into an active contract name. story_4ca6cb1b.
func (s *Server) resolveConfigurationProposed(ctx context.Context, configurationID *string, memberships []string) ([]string, error) {
	if configurationID == nil || *configurationID == "" || s.docs == nil {
		return nil, nil
	}
	id := *configurationID
	cfgDoc, err := s.docs.GetByID(ctx, id, memberships)
	if err != nil {
		return nil, fmt.Errorf("configuration_id %q does not resolve to an active document", id)
	}
	if cfgDoc.Type != document.TypeConfiguration {
		return nil, fmt.Errorf("configuration_id %q is type=%s, want type=%s", id, cfgDoc.Type, document.TypeConfiguration)
	}
	if cfgDoc.Status != document.StatusActive {
		return nil, fmt.Errorf("configuration_id %q is not active (status=%s)", id, cfgDoc.Status)
	}
	cfg, err := document.UnmarshalConfiguration(cfgDoc.Structured)
	if err != nil {
		return nil, fmt.Errorf("configuration_id %q payload decode: %w", id, err)
	}
	if len(cfg.ContractRefs) == 0 {
		return nil, nil
	}
	// Contract documents may be system-scope (empty workspace_id), which
	// the membership filter would reject. Resolution of contract refs
	// must look across scopes — the Configuration doc itself was already
	// access-checked above. Mirrors the nil-memberships pattern in
	// findContractDocByName.
	out := make([]string, 0, len(cfg.ContractRefs))
	for _, ref := range cfg.ContractRefs {
		doc, err := s.docs.GetByID(ctx, ref, nil)
		if err != nil {
			return nil, fmt.Errorf("configuration_id %q: contract_ref %q not found", id, ref)
		}
		if doc.Type != document.TypeContract {
			return nil, fmt.Errorf("configuration_id %q: contract_ref %q is type=%s, want type=contract", id, ref, doc.Type)
		}
		if doc.Status != document.StatusActive {
			return nil, fmt.Errorf("configuration_id %q: contract_ref %q is not active", id, ref)
		}
		out = append(out, doc.Name)
	}
	return out, nil
}

// resolveAgentDefaultConfigurationID looks up the agent document and
// returns its default_configuration_id (empty string when the agent
// doesn't have one set). Returns an error when the supplied agentID
// doesn't resolve to an active type=agent document. story_fb600b97.
func (s *Server) resolveAgentDefaultConfigurationID(ctx context.Context, agentID string, memberships []string) (string, error) {
	if agentID == "" || s.docs == nil {
		return "", nil
	}
	doc, err := s.docs.GetByID(ctx, agentID, memberships)
	if err != nil {
		return "", fmt.Errorf("agent_id %q does not resolve to an active document", agentID)
	}
	if doc.Type != document.TypeAgent {
		return "", fmt.Errorf("agent_id %q is type=%s, want type=agent", agentID, doc.Type)
	}
	if doc.Status != document.StatusActive {
		return "", fmt.Errorf("agent_id %q is not active", agentID)
	}
	settings, err := document.UnmarshalAgentSettings(doc.Structured)
	if err != nil {
		return "", fmt.Errorf("agent_id %q settings decode: %w", agentID, err)
	}
	if settings.DefaultConfigurationID == nil {
		return "", nil
	}
	return *settings.DefaultConfigurationID, nil
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

func specSlotRequired(spec contract.WorkflowSpec, name string) bool {
	for _, slot := range spec.Slots {
		if slot.ContractName == name {
			return slot.Required
		}
	}
	return false
}

// expandDefaultProposed produces a proposed list from a spec using
// each required slot's MinCount.
func expandDefaultProposed(spec contract.WorkflowSpec) []string {
	out := make([]string, 0, len(spec.Slots))
	for _, slot := range spec.Slots {
		if !slot.Required {
			continue
		}
		n := slot.MinCount
		if n <= 0 {
			n = 1
		}
		for i := 0; i < n; i++ {
			out = append(out, slot.ContractName)
		}
	}
	return out
}

// marshalSpecError renders a *contract.SpecError as a JSON tool-result
// text. Non-spec errors are wrapped with a generic shape so callers can
// still parse them.
func marshalSpecError(err error) string {
	var se *contract.SpecError
	if errors.As(err, &se) {
		b, _ := json.Marshal(map[string]any{
			"error":         se.Kind,
			"contract_name": se.ContractName,
			"count":         se.Count,
			"min":           se.Min,
			"max":           se.Max,
			"message":       se.Error(),
		})
		return string(b)
	}
	b, _ := json.Marshal(map[string]any{
		"error":   "invalid_spec",
		"message": err.Error(),
	})
	return string(b)
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
