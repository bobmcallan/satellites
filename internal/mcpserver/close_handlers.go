package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
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
//
// reviewIterationCap is the max number of CIs of the same contract
// type a single story may carry before the rejection-append path
// gives up and flips the story to blocked
// (epic:v4-lifecycle-refactor sty_bbe732af). Env override:
// SATELLITES_REVIEW_ITERATION_CAP.
const (
	resumeCapCI        = 5
	resumeCapStory     = 10
	reviewIterationCap = 3
)

// validationModeKVKey is the KV row key the close handler resolves to
// pick the per-CI validation mode (epic:v4-lifecycle-refactor
// sty_e20e1537). The system-tier seed at boot writes "task" so the
// production default goes through the review-task gate; project /
// user / workspace overrides flip individual CIs back to the legacy
// inline reviewer modes.
const validationModeKVKey = "lifecycle.validation_mode"

// DefaultValidationMode is the legacy fallback applied when no KV
// row resolves AND the contract document carries no validation_mode
// field. Production defaults to ModeTask via the system-tier KV seed
// (see configseed in cmd/satellites/main.go); this constant is the
// "didn't even get the seed" backstop.
var DefaultValidationMode = reviewer.ModeTask

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
				"message":              "plan close requires at least one task enqueued against this CI; call task_enqueue with contract_instance_id before close",
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

	// epic:v4-lifecycle-refactor sty_e20e1537: every close goes through
	// the review-task gate. resolveValidationMode walks the KV chain
	// (system → user → project → workspace, default ModeTask) and
	// falls back to the contract doc's validation_mode field. When the
	// resolved mode is ModeTask AND a task store is wired, the close
	// enqueues a kind:review task and flips the CI to pending_review;
	// a reviewer-role runtime then calls contract_review_close to
	// commit the verdict.
	mode := s.resolveValidationMode(ctx, ci, caller.UserID, memberships)
	if mode == reviewer.ModeTask && s.tasks != nil {
		reviewTaskID, terr := s.enqueueReviewTask(ctx, ci, closeRow.ID, evidenceRowID, caller.UserID, now)
		if terr != nil {
			return mcpgo.NewToolResultError(fmt.Sprintf("review-task enqueue: %v", terr)), nil
		}
		if _, err := s.contracts.UpdateStatus(ctx, ci.ID, contract.StatusPendingReview, caller.UserID, now, memberships); err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
		body, _ := json.Marshal(map[string]any{
			"contract_instance_id": ci.ID,
			"story_id":             ci.StoryID,
			"status":               contract.StatusPendingReview,
			"close_ledger_id":      closeRow.ID,
			"evidence_ledger_id":   evidenceRowID,
			"plan_ledger_id":       planRowID,
			"review_task_id":       reviewTaskID,
			"validation_mode":      mode,
		})
		s.logger.Info().
			Str("method", "tools/call").
			Str("tool", "contract_close").
			Str("ci_id", ci.ID).
			Str("status", contract.StatusPendingReview).
			Str("review_task_id", reviewTaskID).
			Str("validation_mode", mode).
			Int64("duration_ms", time.Since(start).Milliseconds()).
			Msg("mcp tool call")
		return mcpgo.NewToolResultText(string(body)), nil
	}

	// Fallback path (no task store wired, or non-task mode resolved
	// despite the inline reviewer's removal): auto-pass the CI after
	// writing the close-request + evidence rows. Per sty_e20e1537 the
	// inline reviewer (runReviewer) is gone — non-task modes survive
	// only as a deprecated tag on the contract doc / ledger; the close
	// path no longer dispatches to gemini in-line. Tests that don't
	// wire a task store rely on this branch; production always wires
	// tasks so the task-gate above is the only live path.
	if _, err := s.contracts.UpdateStatus(ctx, ci.ID, contract.StatusPassed, caller.UserID, now, memberships); err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}

	// sty_dc121948: the close path no longer writes story status.
	// The internal/storystatus reconciler subscribed to kind:close-
	// request / kind:verdict events recomputes derived status from
	// CI rows and writes via UpdateStatusDerived. Response shape
	// preserves story_status="" for client back-compat — clients now
	// refresh from story_get or watch the WS hub for the next
	// story.<status> event.
	storyStatus := ""

	body, _ := json.Marshal(map[string]any{
		"contract_instance_id": ci.ID,
		"story_id":             ci.StoryID,
		"status":               contract.StatusPassed,
		"close_ledger_id":      closeRow.ID,
		"evidence_ledger_id":   evidenceRowID,
		"plan_ledger_id":       planRowID,
		"story_status":         storyStatus,
		"validation_mode":      mode,
	})
	s.logger.Info().
		Str("method", "tools/call").
		Str("tool", "contract_close").
		Str("ci_id", ci.ID).
		Str("validation_mode", mode).
		Int64("duration_ms", time.Since(start).Milliseconds()).
		Msg("mcp tool call")
	return mcpgo.NewToolResultText(string(body)), nil
}

// resolveValidationMode picks the validation_mode for ci by walking
// the KV chain in override-friendly order (user → project →
// workspace → system), then falling back to the contract document's
// structured `validation_mode` field, then to DefaultValidationMode
// (ModeTask).
//
// The walk order differs from ledger.KVResolveScoped's "system always
// wins" semantic: per sty_e20e1537 the system seed is the production
// *default*, but project / user / workspace overrides MUST be able to
// pin a CI back to the legacy reviewer modes for projects that aren't
// ready to migrate. epic:v4-lifecycle-refactor sty_e20e1537.
func (s *Server) resolveValidationMode(ctx context.Context, ci contract.ContractInstance, callerUserID string, memberships []string) string {
	if s.ledger != nil {
		// Append the system sentinel so KV system-tier rows
		// (workspace_id="") are visible to the projection scans below.
		kvMembers := append([]string{""}, memberships...)

		tiers := []ledger.KVProjectionOptions{}
		if callerUserID != "" && ci.WorkspaceID != "" {
			tiers = append(tiers, ledger.KVProjectionOptions{Scope: ledger.KVScopeUser, WorkspaceID: ci.WorkspaceID, UserID: callerUserID})
		}
		if ci.ProjectID != "" {
			tiers = append(tiers, ledger.KVProjectionOptions{Scope: ledger.KVScopeProject, WorkspaceID: ci.WorkspaceID, ProjectID: ci.ProjectID})
		}
		if ci.WorkspaceID != "" {
			tiers = append(tiers, ledger.KVProjectionOptions{Scope: ledger.KVScopeWorkspace, WorkspaceID: ci.WorkspaceID})
		}
		tiers = append(tiers, ledger.KVProjectionOptions{Scope: ledger.KVScopeSystem})

		for _, tier := range tiers {
			rows, err := ledger.KVProjectionScoped(ctx, s.ledger, tier, kvMembers)
			if err != nil {
				continue
			}
			if row, present := rows[validationModeKVKey]; present {
				if v := strings.TrimSpace(row.Value); v != "" {
					return v
				}
			}
		}
	}
	// Deprecated tier-3 fallback: contract document's structured
	// validation_mode field (the pre-sty_e20e1537 shape). New seeds
	// should not carry this; existing rows continue to work until
	// migrated.
	if s.docs != nil && ci.ContractID != "" {
		if doc, err := s.docs.GetByID(ctx, ci.ContractID, nil); err == nil {
			var payload struct {
				ValidationMode string `json:"validation_mode"`
			}
			if len(doc.Structured) > 0 {
				_ = json.Unmarshal(doc.Structured, &payload)
			}
			if v := strings.TrimSpace(payload.ValidationMode); v != "" {
				return v
			}
		}
	}
	return DefaultValidationMode
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

	// epic:roleless-agents — grant resolution removed. Claim/RebindGrant
	// receive an empty grant id; the CI's agent_id (stamped at compose
	// time) is the authoritative binding.
	newGrantID := ""

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

// lookupReviewerAgentID returns the doc id of the system-scope reviewer
// agent that reviews the given contract. Mirrors lookupReviewerAgentBody
// but returns id rather than body — callers that need to stamp
// AgentID onto a review task (sty_c6d76a5b) use this. Empty when no
// matching agent doc exists.
func (s *Server) lookupReviewerAgentID(ctx context.Context, contractName string, _ []string) string {
	agentName := "story_reviewer"
	if contractName == "develop" {
		agentName = "development_reviewer"
	}
	if s.docs == nil {
		return ""
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
			return r.ID
		}
	}
	return ""
}

// enqueueReviewTask makes a kind:review task ready for the embedded
// reviewer service to claim. The reviewer service subscribes to
// kind:review tasks via the task store listener and sources the
// close-request + evidence rows by walking the ledger filtered by
// ContractInstanceID — no per-task Payload required (sty_c6d76a5b:
// "tasks are thin; ledger rows are the artifacts"). Returns the
// resulting task id for caller inclusion in the close response.
//
// sty_c6d76a5b: orchestrator_compose_plan now emits a planned review
// task per CI at compose time. This handler prefers to find that
// pre-existing task and publish it (planned → published). When no
// planned review task exists (legacy in-flight CIs created before
// paired emission landed, or test fixtures that bypass compose_plan),
// the handler falls back to creating one inline.
func (s *Server) enqueueReviewTask(ctx context.Context, ci contract.ContractInstance, closeRowID, evidenceRowID, actor string, now time.Time) (string, error) {
	memberships := []string{ci.WorkspaceID}
	planned, perr := s.tasks.List(ctx, task.ListOptions{
		ContractInstanceID: ci.ID,
		Kind:               task.KindReview,
		Status:             task.StatusPlanned,
	}, memberships)
	if perr == nil && len(planned) > 0 {
		existing := planned[0]
		published, err := s.tasks.Publish(ctx, existing.ID, now, memberships)
		if err != nil {
			return "", fmt.Errorf("publish planned review task: %w", err)
		}
		if s.ledger != nil {
			_, _ = s.ledger.Append(ctx, ledger.LedgerEntry{
				WorkspaceID: ci.WorkspaceID,
				ProjectID:   ci.ProjectID,
				StoryID:     ledger.StringPtr(ci.StoryID),
				ContractID:  ledger.StringPtr(ci.ID),
				Type:        ledger.TypeDecision,
				Tags:        []string{"kind:review-task-published", "phase:" + ci.ContractName, "task_id:" + published.ID},
				Content:     fmt.Sprintf("review task %s published for ci %s (%s) — close=%s evidence=%s", published.ID, ci.ID, ci.ContractName, closeRowID, evidenceRowID),
				CreatedBy:   actor,
			}, now)
		}
		return published.ID, nil
	}

	seed := s.stampTaskIteration(ctx, task.Task{
		WorkspaceID:        ci.WorkspaceID,
		ProjectID:          ci.ProjectID,
		StoryID:            ci.StoryID,
		ContractInstanceID: ci.ID,
		Kind:               task.KindReview,
		Action:             task.ContractAction(ci.ContractName),
		Description:        fmt.Sprintf("review %s", ci.ContractName),
		Origin:             task.OriginStoryStage,
		Priority:           task.PriorityMedium,
	}, nil)
	t, err := s.tasks.Enqueue(ctx, seed, now)
	if err != nil {
		return "", err
	}
	if s.ledger != nil {
		_, _ = s.ledger.Append(ctx, ledger.LedgerEntry{
			WorkspaceID: ci.WorkspaceID,
			ProjectID:   ci.ProjectID,
			StoryID:     ledger.StringPtr(ci.StoryID),
			ContractID:  ledger.StringPtr(ci.ID),
			Type:        ledger.TypeDecision,
			Tags:        []string{"kind:review-task-enqueued", "phase:" + ci.ContractName, "task_id:" + t.ID},
			Content:     fmt.Sprintf("review task %s enqueued for ci %s (%s) — close=%s evidence=%s", t.ID, ci.ID, ci.ContractName, closeRowID, evidenceRowID),
			CreatedBy:   actor,
		}, now)
	}
	return t.ID, nil
}

// ReviewCommitResult is the structured outcome of a verdict commit.
// The MCP handler renders it as JSON; the embedded reviewer service
// uses the fields for log + ledger annotations.
type ReviewCommitResult struct {
	ContractInstanceID string
	StoryID            string
	Status             string
	Verdict            string
	VerdictLedgerID    string
	ReviewTaskID       string
	StoryStatus        string
	AppendedCIID       string
	BlockedReason      string
}

// handleContractReviewClose is the MCP-facing wrapper around
// CommitReviewVerdict. It parses the request, resolves the caller's
// memberships, calls the commit, and renders the structured result as
// a JSON tool response. epic:v4-lifecycle-refactor sty_b6b2de01.
func (s *Server) handleContractReviewClose(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	caller, _ := UserFrom(ctx)
	ciID, err := req.RequireString("contract_instance_id")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	verdictArg, err := req.RequireString("verdict")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	rationale := req.GetString("rationale", "")
	reviewTaskID := req.GetString("review_task_id", "")
	memberships := s.resolveCallerMemberships(ctx, caller)
	res, cerr := s.CommitReviewVerdict(ctx, ciID, verdictArg, rationale, reviewTaskID, caller.UserID, s.nowUTC(), memberships)
	if cerr != nil {
		return mcpgo.NewToolResultError(cerr.Error()), nil
	}
	body, _ := json.Marshal(map[string]any{
		"contract_instance_id": res.ContractInstanceID,
		"story_id":             res.StoryID,
		"status":               res.Status,
		"verdict":              res.Verdict,
		"verdict_ledger_id":    res.VerdictLedgerID,
		"review_task_id":       res.ReviewTaskID,
		"story_status":         res.StoryStatus,
		"appended_ci_id":       res.AppendedCIID,
		"blocked_reason":       res.BlockedReason,
	})
	s.logger.Info().
		Str("method", "tools/call").
		Str("tool", "contract_review_close").
		Str("ci_id", res.ContractInstanceID).
		Str("verdict", res.Verdict).
		Str("status", res.Status).
		Str("appended_ci_id", res.AppendedCIID).
		Msg("mcp tool call")
	return mcpgo.NewToolResultText(string(body)), nil
}

// CommitReviewVerdict applies a reviewer's verdict to a pending_review
// CI. Shared between the MCP handler (handleContractReviewClose) and
// the embedded reviewer service goroutine (internal/reviewer/service)
// so both paths produce identical ledger + state transitions.
//
// Steps:
//
//   - validate the verdict argument (accepted | rejected only).
//   - look up the CI; reject when not in pending_review.
//   - write a kind:verdict ledger row carrying outcome + rationale.
//   - flip the CI to passed (accepted) or failed (rejected).
//   - close the originating review task when reviewTaskID is non-empty.
//   - on rejected: append a fresh CI of the same contract type with
//     PriorCIID set, OR escalate the story to blocked when the
//     iteration cap is exceeded (sty_bbe732af).
//   - on accepted: walk the story to done when every required CI is
//     terminal.
//
// memberships scopes the writes; pass nil for system-identity callers
// (e.g. the embedded reviewer service running cross-workspace).
//
// epic:v4-lifecycle-refactor sty_b6b2de01 / sty_6077711d.
func (s *Server) CommitReviewVerdict(
	ctx context.Context,
	ciID, verdictArg, rationale, reviewTaskID, actor string,
	now time.Time,
	memberships []string,
) (ReviewCommitResult, error) {
	out := ReviewCommitResult{
		ContractInstanceID: ciID,
		Verdict:            verdictArg,
		ReviewTaskID:       reviewTaskID,
	}
	if verdictArg != reviewer.VerdictAccepted && verdictArg != reviewer.VerdictRejected {
		body, _ := json.Marshal(map[string]any{
			"error":   "invalid_verdict",
			"verdict": verdictArg,
			"message": "verdict must be one of: accepted | rejected (needs_more is not valid for review_close — the reviewer must commit)",
		})
		return out, errors.New(string(body))
	}
	ci, err := s.contracts.GetByID(ctx, ciID, memberships)
	if err != nil {
		body, _ := json.Marshal(map[string]any{"error": "ci_not_found"})
		return out, errors.New(string(body))
	}
	out.StoryID = ci.StoryID
	if ci.Status != contract.StatusPendingReview {
		body, _ := json.Marshal(map[string]any{
			"error":  "ci_not_pending_review",
			"status": ci.Status,
		})
		return out, errors.New(string(body))
	}

	verdictRow, err := s.writeVerdictRow(ctx, ci, reviewer.Verdict{
		Outcome:   verdictArg,
		Rationale: rationale,
	}, actor, now, map[string]any{
		"mode":           reviewer.ModeTask,
		"review_task_id": reviewTaskID,
	})
	if err != nil {
		return out, err
	}
	out.VerdictLedgerID = verdictRow

	target := contract.StatusPassed
	if verdictArg == reviewer.VerdictRejected {
		target = contract.StatusFailed
	}
	if _, err := s.contracts.UpdateStatus(ctx, ci.ID, target, actor, now, memberships); err != nil {
		return out, err
	}
	out.Status = target

	if reviewTaskID != "" && s.tasks != nil {
		outcome := task.OutcomeSuccess
		if verdictArg == reviewer.VerdictRejected {
			outcome = task.OutcomeFailure
		}
		_, _ = s.tasks.Close(ctx, reviewTaskID, outcome, now, memberships)
	}

	if verdictArg == reviewer.VerdictRejected {
		cap := intEnv("SATELLITES_REVIEW_ITERATION_CAP", reviewIterationCap)
		peers, _ := s.contracts.List(ctx, ci.StoryID, memberships)
		sameType := 0
		for _, p := range peers {
			if p.ContractName == ci.ContractName {
				sameType++
			}
		}
		if sameType >= cap {
			out.BlockedReason = fmt.Sprintf("review iteration cap %d exceeded for contract %q", cap, ci.ContractName)
			if s.stories != nil {
				_, _ = s.stories.UpdateStatus(ctx, ci.StoryID, story.StatusBlocked, actor, now, memberships)
			}
			_, _ = s.ledger.Append(ctx, ledger.LedgerEntry{
				WorkspaceID: ci.WorkspaceID,
				ProjectID:   ci.ProjectID,
				StoryID:     ledger.StringPtr(ci.StoryID),
				Type:        ledger.TypeDecision,
				Tags:        []string{"kind:story-blocked", "phase:" + ci.ContractName, "reason:iteration_cap_exceeded"},
				Content:     out.BlockedReason,
				CreatedBy:   actor,
			}, now)
		} else {
			newCI, _, mintErr := s.mintSuccessorCI(ctx, ci, "kind:rejection-append", sameType+1, fmt.Sprintf("appended fresh %s CI after rejection of %s; reason: %s", ci.ContractName, ci.ID, rationale), actor, now, memberships)
			if mintErr == nil {
				out.AppendedCIID = newCI.ID
			}
		}
	}

	// sty_dc121948: see handleContractClose comment above. The
	// reconciler is the single writer for story status; the close
	// path no longer races it.
	_ = verdictArg
	return out, nil
}

// mintSuccessorCI creates a fresh CI at the same workflow slot as
// prior, copying ContractID, ContractName, Sequence, RequiredForClose,
// and stamping PriorCIID + status=ready. The audit row is written with
// kindTag (`kind:rejection-append` for the review-rejection loop;
// `kind:cancellation-append` for the contract_cancel escape hatch).
// Returns the new CI, the audit row id, and any error from the mint or
// the audit append.
//
// sty_3a59a6d7 — extracted from CommitReviewVerdict so contract_cancel
// can reuse the same successor-mint contract.
func (s *Server) mintSuccessorCI(
	ctx context.Context,
	prior contract.ContractInstance,
	kindTag string,
	iteration int,
	auditContent string,
	actor string,
	now time.Time,
	memberships []string,
) (contract.ContractInstance, string, error) {
	newCI, cerr := s.contracts.Create(ctx, contract.ContractInstance{
		WorkspaceID:      prior.WorkspaceID,
		ProjectID:        prior.ProjectID,
		StoryID:          prior.StoryID,
		ContractID:       prior.ContractID,
		ContractName:     prior.ContractName,
		Sequence:         prior.Sequence,
		RequiredForClose: prior.RequiredForClose,
		Status:           contract.StatusReady,
		PriorCIID:        prior.ID,
	}, now)
	if cerr != nil {
		return contract.ContractInstance{}, "", cerr
	}
	row, lerr := s.ledger.Append(ctx, ledger.LedgerEntry{
		WorkspaceID: prior.WorkspaceID,
		ProjectID:   prior.ProjectID,
		StoryID:     ledger.StringPtr(prior.StoryID),
		ContractID:  ledger.StringPtr(newCI.ID),
		Type:        ledger.TypeDecision,
		Tags:        []string{kindTag, "phase:" + prior.ContractName, "prior_ci_id:" + prior.ID, "iteration:" + strconv.Itoa(iteration)},
		Content:     auditContent,
		CreatedBy:   actor,
	}, now)
	if lerr != nil {
		return newCI, "", lerr
	}
	return newCI, row.ID, nil
}

// ensureCloseHandlersCompile references the error + fmt packages to
// keep imports pinned even when code paths shift.
var _ = errors.New
var _ = fmt.Sprintf
