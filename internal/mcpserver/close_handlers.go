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
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/reviewer"
	"github.com/bobmcallan/satellites/internal/story"
	"github.com/bobmcallan/satellites/internal/task"
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

// handleContractClose closes a CI. Always writes a phase:close
// row; when evidence_markdown non-empty also writes a kind:evidence
// row; optionally writes a plan row on deferred plan; flips CI to
// passed; rolls story to done when all required CIs are terminal.
func (s *Server) handleContractClose(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
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

	// Plan CIs must enqueue at least one child task before close — a
	// plan that decomposes nothing is not a plan. Gate is skipped when
	// no task store is wired (early-boot tests, minimal fixtures).
	// epic:v4-lifecycle-refactor sty_0c21a0cf.
	if ci.ContractName == "plan" && s.tasks != nil {
		linked, lerr := s.tasks.List(ctx, task.ListOptions{ContractInstanceID: ci.ID}, memberships)
		if lerr != nil {
			return mcpgo.NewToolResultError(lerr.Error()), nil
		}
		if len(linked) == 0 {
			body, _ := json.Marshal(map[string]any{
				"error":                "plan_close_requires_tasks",
				"contract_instance_id": ci.ID,
				"message":              "plan close requires at least one task enqueued against this CI; call task_enqueue with contract_instance_id + required_role before close",
			})
			return mcpgo.NewToolResultError(string(body)), nil
		}
	}

	now := s.nowUTC()

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

	// For ready CIs (close pre-claim): transition ready→claimed
	// before passing, because ValidTransition rejects ready→passed.
	// grantID is empty here — the CI flips to passed immediately below
	// so the binding is ephemeral.
	if ci.Status == contract.StatusReady {
		if _, err := s.contracts.Claim(ctx, ci.ID, "", now, memberships); err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
	}

	// Reviewer branch — consult the contract document's
	// validation_mode. On needs_more the close is rejected; on
	// accepted/rejected the CI is transitioned accordingly.
	verdictOutcome, verdictRowID, llmUsageRowID, err := s.runReviewer(ctx, ci, evidenceMarkdown, evidenceIDs, caller.UserID, now, memberships)
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	switch verdictOutcome {
	case reviewer.VerdictNeedsMore:
		// CI stays claimed; structured error names the unresolved
		// review questions so the agent can call contract_respond
		// + re-close.
		body, _ := json.Marshal(map[string]any{
			"error":             "needs_more",
			"verdict_ledger_id": verdictRowID,
			"message":           "reviewer needs more; call contract_respond then re-invoke close",
		})
		return mcpgo.NewToolResultError(string(body)), nil
	case reviewer.VerdictRejected:
		if _, err := s.contracts.UpdateStatus(ctx, ci.ID, contract.StatusFailed, caller.UserID, now, memberships); err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
	default: // accepted | skip (no-verdict path for mode=agent)
		if _, err := s.contracts.UpdateStatus(ctx, ci.ID, contract.StatusPassed, caller.UserID, now, memberships); err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
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

	finalStatus := contract.StatusPassed
	if verdictOutcome == reviewer.VerdictRejected {
		finalStatus = contract.StatusFailed
	}
	body, _ := json.Marshal(map[string]any{
		"contract_instance_id": ci.ID,
		"story_id":             ci.StoryID,
		"status":               finalStatus,
		"close_ledger_id":      closeRow.ID,
		"evidence_ledger_id":   evidenceRowID,
		"plan_ledger_id":       planRowID,
		"story_status":         storyStatus,
		"verdict_ledger_id":    verdictRowID,
		"llm_usage_ledger_id":  llmUsageRowID,
		"verdict":              verdictOutcome,
	})
	s.logger.Info().
		Str("method", "tools/call").
		Str("tool", "contract_close").
		Str("ci_id", ci.ID).
		Int64("duration_ms", time.Since(start).Milliseconds()).
		Msg("mcp tool call")
	return mcpgo.NewToolResultText(string(body)), nil
}

// handleContractRespond writes a kind:review-response ledger row
// targeting the latest unresolved review-question (if any). The
// reviewer re-invocation lives in slice 8.5; this verb only persists
// the response.
func (s *Server) handleContractRespond(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
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
	}, s.nowUTC())
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

// handleContractResume is the extended resume verb:
//   - verifies session is registered + fresh.
//   - enforces per-CI + per-story resume caps via ledger kv counters.
//   - reopens passed CIs: flips to claimed, dereferences prior plan +
//     action-claim rows, flips downstream required CIs back to ready.
//   - rebinds session on claimed CIs.
//   - writes a kind:resume ledger row.
func (s *Server) handleContractResume(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
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
	if err := s.verifyCallerSession(ctx, caller.UserID, sessionID, s.nowUTC()); err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}

	// Resolve the incoming session's orchestrator grant so Claim /
	// RebindGrant / downstream-rollback writes all carry the grant
	// binding (story_4608a82c). A gate error here is fatal — resume
	// onto a session whose grant doesn't cover the CI's required_role
	// is rejected the same way claim rejects it.
	newGrantID, gateErr := s.resolveRequiredRoleGrant(ctx, ci, caller.UserID, sessionID)
	if gateErr != nil {
		return mcpgo.NewToolResultError(gateErr.Error()), nil
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

	now := s.nowUTC()
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
		// Flip CI back to ready + clear claim fields, then claim anew
		// under the new session's grant.
		if _, err := s.contracts.ClearClaim(ctx, ci.ID, now, memberships); err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
		if _, err := s.contracts.Claim(ctx, ci.ID, newGrantID, now, memberships); err != nil {
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
		if _, err := s.contracts.RebindGrant(ctx, ci.ID, newGrantID, now, memberships); err != nil {
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

// runReviewer dispatches on the CI's contract document's
// validation_mode and writes the verdict + (for llm mode) llm-usage
// rows. Returns the verdict outcome string, the verdict row id, the
// llm-usage row id, and any error. A verdict outcome of "" means "no
// reviewer ran" (agent mode or the contract doc is unreadable) and
// the caller treats that as an accepted-equivalent.
func (s *Server) runReviewer(
	ctx context.Context,
	ci contract.ContractInstance,
	evidenceMarkdown string,
	evidenceLedgerIDs []string,
	actor string,
	now time.Time,
	memberships []string,
) (string, string, string, error) {
	contractDoc, err := s.docs.GetByID(ctx, ci.ContractID, nil)
	if err != nil {
		// Can't read the contract doc → treat as agent mode (no
		// verdict row). The outer close path still writes evidence +
		// close-request; this just skips reviewer invocation.
		return "", "", "", nil
	}
	mode, checks := parseContractStructured(contractDoc.Structured)
	switch mode {
	case reviewer.ModeCheckBased:
		input := s.gatherChecksInput(ctx, ci, memberships)
		verdict, outcomes := reviewer.RunChecks(checks, input)
		rowID, err := s.writeVerdictRow(ctx, ci, verdict, actor, now, map[string]any{
			"mode":     reviewer.ModeCheckBased,
			"outcomes": outcomes,
		})
		return verdict.Outcome, rowID, "", err
	case reviewer.ModeLLM:
		req := reviewer.Request{
			ContractID:       contractDoc.ID,
			ContractName:     contractDoc.Name,
			AgentInstruction: contractDoc.Body,
			ReviewerRubric:   s.lookupReviewerAgentBody(ctx, ci.ContractName, memberships),
			EvidenceMarkdown: evidenceMarkdown,
			EvidenceRefs:     evidenceLedgerIDs,
			ACScope:          ci.ACScope,
		}
		verdict, usage, err := s.reviewer.Review(ctx, req)
		if err != nil {
			return "", "", "", fmt.Errorf("reviewer: %w", err)
		}
		var usageRowID string
		usageRowID, _ = s.writeLLMUsageRow(ctx, ci, usage, actor, now)
		rowID, err := s.writeVerdictRow(ctx, ci, verdict, actor, now, map[string]any{
			"mode":             reviewer.ModeLLM,
			"principles_cited": verdict.PrinciplesCited,
			"review_questions": verdict.ReviewQuestions,
			"model":            usage.Model,
			"cost_usd":         usage.CostUSD,
		})
		if err != nil {
			return "", "", "", err
		}
		// On needs_more write one kind:review-question row per item so
		// contract_respond can target them.
		if verdict.Outcome == reviewer.VerdictNeedsMore {
			for _, q := range verdict.ReviewQuestions {
				_, _ = s.ledger.Append(ctx, ledger.LedgerEntry{
					WorkspaceID: ci.WorkspaceID,
					ProjectID:   ci.ProjectID,
					StoryID:     ledger.StringPtr(ci.StoryID),
					ContractID:  ledger.StringPtr(ci.ID),
					Type:        ledger.TypeDecision,
					Tags:        []string{"kind:review-question", "phase:" + ci.ContractName},
					Content:     q,
					CreatedBy:   actor,
				}, now)
			}
		}
		return verdict.Outcome, rowID, usageRowID, nil
	default:
		// agent mode (or missing). No verdict row; caller proceeds as
		// accepted.
		return "", "", "", nil
	}
}

// parseContractStructured reads validation_mode + checks from a
// contract document's structured field. Tolerant of unknown JSON —
// returns "" when structured is empty or malformed.
func parseContractStructured(raw []byte) (string, []reviewer.Check) {
	if len(raw) == 0 {
		return "", nil
	}
	var payload struct {
		ValidationMode string           `json:"validation_mode"`
		Checks         []reviewer.Check `json:"checks"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", nil
	}
	return payload.ValidationMode, payload.Checks
}

// gatherChecksInput collects the artifact names already present on
// the CI's ledger. Used by the check-based runner's artifact_exists
// check.
func (s *Server) gatherChecksInput(ctx context.Context, ci contract.ContractInstance, memberships []string) reviewer.ChecksInput {
	input := reviewer.ChecksInput{Artifacts: map[string]bool{}}
	rows, err := s.ledger.List(ctx, ci.ProjectID, ledger.ListOptions{
		Type: ledger.TypeArtifact,
	}, memberships)
	if err != nil {
		return input
	}
	for _, r := range rows {
		if r.ContractID == nil || *r.ContractID != ci.ID {
			continue
		}
		for _, tag := range r.Tags {
			const prefix = "artifact:"
			if len(tag) > len(prefix) && tag[:len(prefix)] == prefix {
				input.Artifacts[tag[len(prefix):]] = true
			}
		}
	}
	return input
}

// writeVerdictRow appends a kind:verdict ledger row carrying the
// reviewer's outcome + rationale + structured metadata.
func (s *Server) writeVerdictRow(ctx context.Context, ci contract.ContractInstance, v reviewer.Verdict, actor string, now time.Time, extra map[string]any) (string, error) {
	payload := map[string]any{
		"verdict":   v.Outcome,
		"rationale": v.Rationale,
	}
	for k, val := range extra {
		payload[k] = val
	}
	structured, _ := json.Marshal(payload)
	row, err := s.ledger.Append(ctx, ledger.LedgerEntry{
		WorkspaceID: ci.WorkspaceID,
		ProjectID:   ci.ProjectID,
		StoryID:     ledger.StringPtr(ci.StoryID),
		ContractID:  ledger.StringPtr(ci.ID),
		Type:        ledger.TypeVerdict,
		Tags:        []string{"kind:verdict", "phase:" + ci.ContractName},
		Content:     v.Rationale,
		Structured:  structured,
		CreatedBy:   actor,
	}, now)
	if err != nil {
		return "", err
	}
	return row.ID, nil
}

// writeLLMUsageRow appends a kind:llm-usage decision row consumed by
// the CostRollup derivation from slice 7.3. Skipped when usage is
// zero (no tokens claimed).
func (s *Server) writeLLMUsageRow(ctx context.Context, ci contract.ContractInstance, usage reviewer.UsageCost, actor string, now time.Time) (string, error) {
	if usage.InputTokens == 0 && usage.OutputTokens == 0 && usage.CostUSD == 0 {
		return "", nil
	}
	structured, _ := json.Marshal(map[string]any{
		"input_tokens":  usage.InputTokens,
		"output_tokens": usage.OutputTokens,
		"cost_usd":      usage.CostUSD,
		"model":         usage.Model,
	})
	row, err := s.ledger.Append(ctx, ledger.LedgerEntry{
		WorkspaceID: ci.WorkspaceID,
		ProjectID:   ci.ProjectID,
		StoryID:     ledger.StringPtr(ci.StoryID),
		ContractID:  ledger.StringPtr(ci.ID),
		Type:        ledger.TypeDecision,
		Tags:        []string{"kind:llm-usage", "phase:" + ci.ContractName},
		Content:     "reviewer llm usage",
		Structured:  structured,
		CreatedBy:   actor,
	}, now)
	if err != nil {
		return "", err
	}
	return row.ID, nil
}

// lookupReviewerAgentBody returns the body of the system-scope reviewer
// agent that reviews the given contract. story_b4d1107c
// (epic:configuration-over-code-mandate) routes `develop` to
// `development_reviewer`; everything else to `story_reviewer`. Empty
// when the expected agent doc is missing.
//
// This replaces the prior contract_binding-keyed `lookupReviewerRubric`
// lookup. The new model treats the reviewer rubric as an agent body
// (story_6d259b99 seeded the two reviewer agents) keyed on contract
// name; per pr_no_unrequested_compat the prior helper is deleted, not
// aliased.
//
// Memberships argument is unused: per pr_0779e5af scope=system content
// is globally readable inside the workspace, so the lookup passes nil
// (mirroring `listSystemDocuments` in the portal config view).
func (s *Server) lookupReviewerAgentBody(ctx context.Context, contractName string, _ []string) string {
	agentName := "story_reviewer"
	if contractName == "develop" {
		agentName = "development_reviewer"
	}
	rows, err := s.docs.List(ctx, document.ListOptions{
		Type:  document.TypeAgent,
		Scope: document.ScopeSystem,
	}, nil)
	if err != nil {
		return ""
	}
	for _, r := range rows {
		if r.Status == document.StatusActive && r.Name == agentName {
			return r.Body
		}
	}
	return ""
}

// ensureCloseHandlersCompile references the error + fmt packages to
// keep imports pinned even when code paths shift.
var _ = errors.New
var _ = fmt.Sprintf
