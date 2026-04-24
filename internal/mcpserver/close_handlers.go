package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/story"
)

// resumeCapCI / resumeCapStory are the defaults for the per-CI and
// per-story resume caps. Env overrides:
//
//	SATELLITES_MAX_RESUMES_PER_CI
//	SATELLITES_MAX_RESUMES_PER_STORY
const (
	resumeCapCI    = 5
	resumeCapStory = 10
)

func intEnv(key string, fallback int) int {
	if raw := os.Getenv(key); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

// handleStoryContractClose closes a CI. Always writes a phase:close
// row; when evidence_markdown non-empty also writes a kind:evidence
// row; optionally writes a plan row on preplan re-entry or deferred
// plan; flips CI to passed; rolls story to done when all required
// CIs are terminal. On preplan close with proposed_workflow, the
// agent's new workflow shape is validated against the project spec
// and recorded as a kind:workflow-claim row.
func (s *Server) handleStoryContractClose(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	start := time.Now()
	caller, _ := UserFrom(ctx)
	ciID, err := req.RequireString("contract_instance_id")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	closeMarkdown := req.GetString("close_markdown", "")
	evidenceMarkdown := req.GetString("evidence_markdown", "")
	evidenceIDs := req.GetStringSlice("evidence_ledger_ids", nil)
	planMarkdown := req.GetString("plan_markdown", "")
	proposedWorkflow := req.GetStringSlice("proposed_workflow", nil)

	memberships := s.resolveCallerMemberships(ctx, caller)
	ci, err := s.contracts.GetByID(ctx, ciID, memberships)
	if err != nil {
		body, _ := json.Marshal(map[string]any{"error": "ci_not_found"})
		return mcpgo.NewToolResultError(string(body)), nil
	}
	if ci.Status == contract.StatusPassed || ci.Status == contract.StatusFailed || ci.Status == contract.StatusSkipped {
		body, _ := json.Marshal(map[string]any{"error": "ci_already_terminal", "status": ci.Status})
		return mcpgo.NewToolResultError(string(body)), nil
	}

	now := time.Now().UTC()

	// Preplan proposed_workflow validation — if supplied, must satisfy
	// the project's workflow_spec. Mirrors story_workflow_claim.
	if ci.ContractName == "preplan" && len(proposedWorkflow) > 0 {
		spec, err := s.loadWorkflowSpec(ctx, ci.ProjectID, memberships)
		if err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
		if err := spec.Validate(proposedWorkflow); err != nil {
			return mcpgo.NewToolResultText(marshalSpecError(err)), nil
		}
	}

	// Deferred plan: CI has no PlanLedgerID yet and caller supplied one.
	var planRowID string
	if planMarkdown != "" && ci.PlanLedgerID == "" {
		planRow, err := s.ledger.Append(ctx, ledger.LedgerEntry{
			WorkspaceID: ci.WorkspaceID,
			ProjectID:   ci.ProjectID,
			StoryID:     ledger.StringPtr(ci.StoryID),
			ContractID:  ledger.StringPtr(ci.ID),
			Type:        ledger.TypePlan,
			Tags:        []string{"kind:plan", "phase:" + ci.ContractName},
			Content:     planMarkdown,
			CreatedBy:   caller.UserID,
		}, now)
		if err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
		planRowID = planRow.ID
		planRef := planRowID
		if _, err := s.contracts.UpdateLedgerRefs(ctx, ci.ID, &planRef, nil, caller.UserID, now, memberships); err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
	}

	// Optional evidence row.
	var evidenceRowID string
	if evidenceMarkdown != "" {
		evRow, err := s.ledger.Append(ctx, ledger.LedgerEntry{
			WorkspaceID: ci.WorkspaceID,
			ProjectID:   ci.ProjectID,
			StoryID:     ledger.StringPtr(ci.StoryID),
			ContractID:  ledger.StringPtr(ci.ID),
			Type:        ledger.TypeEvidence,
			Tags:        []string{"kind:evidence", "phase:" + ci.ContractName},
			Content:     evidenceMarkdown,
			CreatedBy:   caller.UserID,
		}, now)
		if err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
		evidenceRowID = evRow.ID
	}

	// Close-request row.
	closeStructured, _ := json.Marshal(map[string]any{
		"evidence_ledger_ids": append([]string{}, evidenceIDs...),
		"evidence_row_id":     evidenceRowID,
	})
	closeRow, err := s.ledger.Append(ctx, ledger.LedgerEntry{
		WorkspaceID: ci.WorkspaceID,
		ProjectID:   ci.ProjectID,
		StoryID:     ledger.StringPtr(ci.StoryID),
		ContractID:  ledger.StringPtr(ci.ID),
		Type:        ledger.TypeCloseRequest,
		Tags:        []string{"kind:close-request", "phase:close"},
		Content:     closeMarkdown,
		Structured:  closeStructured,
		CreatedBy:   caller.UserID,
	}, now)
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}

	// Stamp CloseLedgerID before flipping status so the ref is present
	// on the passed CI.
	closeRef := closeRow.ID
	if _, err := s.contracts.UpdateLedgerRefs(ctx, ci.ID, nil, &closeRef, caller.UserID, now, memberships); err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}

	// For ready CIs (preplan re-entry closes pre-claim): transition
	// ready→claimed before passing, because ValidTransition rejects
	// ready→passed.
	if ci.Status == contract.StatusReady {
		if _, err := s.contracts.Claim(ctx, ci.ID, caller.UserID, now, memberships); err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
	}

	if _, err := s.contracts.UpdateStatus(ctx, ci.ID, contract.StatusPassed, caller.UserID, now, memberships); err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}

	// Preplan workflow-claim — written after the CI close so the claim
	// applies to the next planning pass.
	var workflowClaimID string
	if ci.ContractName == "preplan" && len(proposedWorkflow) > 0 {
		payload, _ := json.Marshal(map[string]any{"proposed_contracts": proposedWorkflow})
		row, err := s.ledger.Append(ctx, ledger.LedgerEntry{
			WorkspaceID: ci.WorkspaceID,
			ProjectID:   ci.ProjectID,
			StoryID:     ledger.StringPtr(ci.StoryID),
			Type:        ledger.TypeWorkflowClaim,
			Tags:        []string{"kind:workflow-claim", "phase:pre-plan", "origin:close"},
			Content:     closeMarkdown,
			Structured:  payload,
			CreatedBy:   caller.UserID,
		}, now)
		if err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
		workflowClaimID = row.ID
	}

	// Story rollup: if every RequiredForClose CI is terminal, flip the
	// story to done.
	storyStatus := ""
	peers, _ := s.contracts.List(ctx, ci.StoryID, memberships)
	allTerminal := true
	for _, p := range peers {
		if !p.RequiredForClose {
			continue
		}
		if p.Status != contract.StatusPassed && p.Status != contract.StatusSkipped {
			allTerminal = false
			break
		}
	}
	if allTerminal {
		storyStatus = s.walkStoryToDone(ctx, ci.StoryID, caller.UserID, now, memberships)
	}

	body, _ := json.Marshal(map[string]any{
		"contract_instance_id":     ci.ID,
		"story_id":                 ci.StoryID,
		"status":                   contract.StatusPassed,
		"close_ledger_id":          closeRow.ID,
		"evidence_ledger_id":       evidenceRowID,
		"plan_ledger_id":           planRowID,
		"workflow_claim_ledger_id": workflowClaimID,
		"story_status":             storyStatus,
	})
	s.logger.Info().
		Str("method", "tools/call").
		Str("tool", "story_contract_close").
		Str("ci_id", ci.ID).
		Int64("duration_ms", time.Since(start).Milliseconds()).
		Msg("mcp tool call")
	return mcpgo.NewToolResultText(string(body)), nil
}

// handleStoryContractRespond writes a kind:review-response ledger row
// targeting the latest unresolved review-question (if any). The
// reviewer re-invocation lives in slice 8.5; this verb only persists
// the response.
func (s *Server) handleStoryContractRespond(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	caller, _ := UserFrom(ctx)
	ciID, err := req.RequireString("contract_instance_id")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	response, err := req.RequireString("response_markdown")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	memberships := s.resolveCallerMemberships(ctx, caller)
	ci, err := s.contracts.GetByID(ctx, ciID, memberships)
	if err != nil {
		body, _ := json.Marshal(map[string]any{"error": "ci_not_found"})
		return mcpgo.NewToolResultError(string(body)), nil
	}
	target := s.findLatestReviewQuestion(ctx, ci, memberships)
	tags := []string{"kind:review-response", "phase:" + ci.ContractName}
	if target != "" {
		tags = append(tags, "target:"+target)
	}
	row, err := s.ledger.Append(ctx, ledger.LedgerEntry{
		WorkspaceID: ci.WorkspaceID,
		ProjectID:   ci.ProjectID,
		StoryID:     ledger.StringPtr(ci.StoryID),
		ContractID:  ledger.StringPtr(ci.ID),
		Type:        ledger.TypeDecision,
		Tags:        tags,
		Content:     response,
		CreatedBy:   caller.UserID,
	}, time.Now().UTC())
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	body, _ := json.Marshal(map[string]any{
		"contract_instance_id":   ci.ID,
		"response_ledger_id":     row.ID,
		"review_question_target": target,
	})
	return mcpgo.NewToolResultText(string(body)), nil
}

// handleStoryContractResume is the extended resume verb:
//   - verifies session is registered + fresh.
//   - enforces per-CI + per-story resume caps via ledger kv counters.
//   - reopens passed CIs: flips to claimed, dereferences prior plan +
//     action-claim rows, flips downstream required CIs back to ready.
//   - rebinds session on claimed CIs.
//   - writes a kind:resume ledger row.
func (s *Server) handleStoryContractResume(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	caller, _ := UserFrom(ctx)
	ciID, err := req.RequireString("contract_instance_id")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	sessionID, err := req.RequireString("session_id")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	reason, err := req.RequireString("reason")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	memberships := s.resolveCallerMemberships(ctx, caller)
	ci, err := s.contracts.GetByID(ctx, ciID, memberships)
	if err != nil {
		body, _ := json.Marshal(map[string]any{"error": "ci_not_found"})
		return mcpgo.NewToolResultError(string(body)), nil
	}
	if err := s.verifyCallerSession(ctx, caller.UserID, sessionID, time.Now().UTC()); err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}

	// Resume caps.
	capCI := intEnv("SATELLITES_MAX_RESUMES_PER_CI", resumeCapCI)
	capStory := intEnv("SATELLITES_MAX_RESUMES_PER_STORY", resumeCapStory)
	ciCount := s.readCounter(ctx, ci.ProjectID, "key:resume_count:ci:"+ci.ID, memberships)
	storyCount := s.readCounter(ctx, ci.ProjectID, "key:resume_count:story:"+ci.StoryID, memberships)
	if ciCount >= capCI {
		body, _ := json.Marshal(map[string]any{"error": "resume_cap_exceeded_ci", "count": ciCount, "cap": capCI})
		return mcpgo.NewToolResultError(string(body)), nil
	}
	if storyCount >= capStory {
		body, _ := json.Marshal(map[string]any{"error": "resume_cap_exceeded_story", "count": storyCount, "cap": capStory})
		return mcpgo.NewToolResultError(string(body)), nil
	}

	now := time.Now().UTC()
	rolledBack := []string{}
	reopen := ci.Status == contract.StatusPassed

	if reopen {
		// Dereference prior plan + action_claim rows on this CI.
		if ci.PlanLedgerID != "" {
			_, _ = s.ledger.Dereference(ctx, ci.PlanLedgerID, "resume: reopen", caller.UserID, now, memberships)
		}
		if priorAC := s.findLatestActionClaim(ctx, ci, memberships); priorAC != "" {
			_, _ = s.ledger.Dereference(ctx, priorAC, "resume: reopen", caller.UserID, now, memberships)
		}
		// Flip CI back to ready + clear claim fields, then claim anew.
		if _, err := s.contracts.ClearClaim(ctx, ci.ID, now, memberships); err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
		if _, err := s.contracts.Claim(ctx, ci.ID, sessionID, now, memberships); err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
		// Downstream rollback: every required CI with Sequence >
		// ci.Sequence whose Status is {passed, claimed} goes back to
		// ready.
		peers, _ := s.contracts.List(ctx, ci.StoryID, memberships)
		for _, p := range peers {
			if p.ID == ci.ID || p.Sequence <= ci.Sequence {
				continue
			}
			if !p.RequiredForClose {
				continue
			}
			if p.Status != contract.StatusPassed && p.Status != contract.StatusClaimed {
				continue
			}
			if _, err := s.contracts.ClearClaim(ctx, p.ID, now, memberships); err != nil {
				continue
			}
			rolledBack = append(rolledBack, p.ID)
		}
	} else if ci.Status == contract.StatusClaimed {
		if _, err := s.contracts.RebindSession(ctx, ci.ID, sessionID, now, memberships); err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
	} else {
		body, _ := json.Marshal(map[string]any{"error": "ci_not_resumable", "status": ci.Status})
		return mcpgo.NewToolResultError(string(body)), nil
	}

	// Increment counters.
	s.writeCounter(ctx, ci, "key:resume_count:ci:"+ci.ID, ciCount+1, caller.UserID, now)
	s.writeCounter(ctx, ci, "key:resume_count:story:"+ci.StoryID, storyCount+1, caller.UserID, now)

	row, err := s.ledger.Append(ctx, ledger.LedgerEntry{
		WorkspaceID: ci.WorkspaceID,
		ProjectID:   ci.ProjectID,
		StoryID:     ledger.StringPtr(ci.StoryID),
		ContractID:  ledger.StringPtr(ci.ID),
		Type:        ledger.TypeDecision,
		Tags:        []string{"kind:resume", "phase:" + ci.ContractName},
		Content:     reason,
		CreatedBy:   caller.UserID,
	}, now)
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	body, _ := json.Marshal(map[string]any{
		"contract_instance_id": ci.ID,
		"resume_ledger_id":     row.ID,
		"session_id":           sessionID,
		"reopened":             reopen,
		"rolled_back_cis":      rolledBack,
		"resume_count_ci":      ciCount + 1,
		"resume_count_story":   storyCount + 1,
	})
	return mcpgo.NewToolResultText(string(body)), nil
}

// walkStoryToDone advances the story through the required intermediate
// statuses (backlog → ready → in_progress → done) until it lands on
// done. Safe when the story is already mid-way through — the loop
// short-circuits when UpdateStatus rejects as an invalid transition.
func (s *Server) walkStoryToDone(ctx context.Context, storyID, actor string, now time.Time, memberships []string) string {
	current, err := s.stories.GetByID(ctx, storyID, memberships)
	if err != nil {
		return ""
	}
	path := map[string]string{
		story.StatusBacklog:    story.StatusReady,
		story.StatusReady:      story.StatusInProgress,
		story.StatusInProgress: story.StatusDone,
	}
	for {
		next, ok := path[current.Status]
		if !ok {
			return current.Status
		}
		updated, err := s.stories.UpdateStatus(ctx, storyID, next, actor, now, memberships)
		if err != nil {
			return current.Status
		}
		current = updated
		if current.Status == story.StatusDone {
			return current.Status
		}
	}
}

// readCounter returns the latest kv counter value for key or 0 when
// absent.
func (s *Server) readCounter(ctx context.Context, projectID, key string, memberships []string) int {
	rows, err := s.ledger.List(ctx, projectID, ledger.ListOptions{
		Type:  ledger.TypeKV,
		Tags:  []string{key},
		Limit: 1,
	}, memberships)
	if err != nil || len(rows) == 0 {
		return 0
	}
	var payload struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(rows[0].Structured, &payload); err != nil {
		return 0
	}
	return payload.Count
}

// writeCounter appends a kv row carrying the new counter value.
func (s *Server) writeCounter(ctx context.Context, ci contract.ContractInstance, key string, value int, actor string, now time.Time) {
	structured, _ := json.Marshal(map[string]any{"count": value, "key": key})
	_, _ = s.ledger.Append(ctx, ledger.LedgerEntry{
		WorkspaceID: ci.WorkspaceID,
		ProjectID:   ci.ProjectID,
		StoryID:     ledger.StringPtr(ci.StoryID),
		Type:        ledger.TypeKV,
		Tags:        []string{key},
		Content:     strconv.Itoa(value),
		Structured:  structured,
		CreatedBy:   actor,
	}, now)
}

// findLatestReviewQuestion returns the id of the most recent
// kind:review-question row scoped to ci. Empty when none exists.
func (s *Server) findLatestReviewQuestion(ctx context.Context, ci contract.ContractInstance, memberships []string) string {
	rows, err := s.ledger.List(ctx, ci.ProjectID, ledger.ListOptions{
		Type: ledger.TypeDecision,
		Tags: []string{"kind:review-question"},
	}, memberships)
	if err != nil {
		return ""
	}
	for _, r := range rows {
		if r.ContractID != nil && *r.ContractID == ci.ID && r.Status == ledger.StatusActive {
			return r.ID
		}
	}
	return ""
}

// ensureCloseHandlersCompile references the error + fmt packages to
// keep imports pinned even when code paths shift.
var _ = errors.New
var _ = fmt.Sprintf
