// orchestrator_submit_plan implements the front-of-lifecycle plan-approval
// loop introduced by epic:configuration-over-code-mandate (story_a5826137).
// The orchestrator submits its proposed plan; the reviewer (Gemini-backed
// in production, AcceptAll in dev/tests) evaluates against the
// story_reviewer rubric; on accepted a kind:plan-approved ledger row is
// written and workflow_claim's precondition (in handleWorkflowClaim) lets
// the rest of the lifecycle proceed.
//
// The loop is bounded by a KV-configurable cap (key:
// `plan_review_max_iterations`, default 5) resolved via the
// system→workspace→project→user chain. iteration > cap returns
// plan_review_iteration_cap_exceeded so the orchestrator escalates to the
// user.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/reviewer"
)

const (
	// PlanApprovedKind is the ledger tag the workflow_claim precondition
	// looks for. Exported so tests in the mcpserver package can match.
	planApprovedKind = "kind:plan-approved"
	// planApprovedPhase is the phase tag scoped to plan-approval rows.
	planApprovedPhase = "phase:plan-approval"
	// planReviewMaxIterationsKey is the KV key the resolver reads to
	// bound the plan-approval loop.
	planReviewMaxIterationsKey = "plan_review_max_iterations"
	// defaultPlanReviewMaxIterations is the fallback cap when no KV row
	// resolves at any tier. Five mirrors Archon's default.
	defaultPlanReviewMaxIterations = 5
)

// handleOrchestratorSubmitPlan implements the satellites_orchestrator_submit_plan verb.
// On accepted it writes a kind:plan-approved ledger row scoped to the
// story; subsequent workflow_claim calls then pass their precondition.
func (s *Server) handleOrchestratorSubmitPlan(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	start := time.Now()
	caller, _ := UserFrom(ctx)

	storyID, err := req.RequireString("story_id")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	planMD := req.GetString("plan_markdown", "")
	proposed := req.GetStringSlice("proposed_contracts", nil)
	iteration := req.GetInt("iteration", 1)
	if iteration < 1 {
		iteration = 1
	}

	memberships := s.resolveCallerMemberships(ctx, caller)
	st, err := s.stories.GetByID(ctx, storyID, memberships)
	if err != nil {
		return mcpgo.NewToolResultError("story not found"), nil
	}

	cap := s.resolvePlanReviewMaxIterations(ctx, st.WorkspaceID, st.ProjectID, caller.UserID)
	if iteration > cap {
		body, _ := json.Marshal(map[string]any{
			"error":      "plan_review_iteration_cap_exceeded",
			"story_id":   storyID,
			"iteration":  iteration,
			"max":        cap,
			"escalation": "raise plan_review_max_iterations KV or narrow the story",
		})
		return mcpgo.NewToolResultError(string(body)), nil
	}

	rubric := s.lookupReviewerAgentBody(ctx, "plan", memberships)
	revReq := reviewer.Request{
		ContractName:     "plan",
		AgentInstruction: "Review the orchestrator's proposed plan against the story's acceptance criteria and the active principles. Reject plans missing the plan front-floor or the story_close end-floor; cite pr_mandate_reviewer_enforced when doing so.",
		ReviewerRubric:   rubric,
		EvidenceMarkdown: planMD,
	}
	verdict, usage, err := s.reviewer.Review(ctx, revReq)
	if err != nil {
		return mcpgo.NewToolResultError(fmt.Sprintf("reviewer: %v", err)), nil
	}

	now := s.nowUTC()

	out := map[string]any{
		"verdict":          verdict.Outcome,
		"rationale":        verdict.Rationale,
		"principles_cited": verdict.PrinciplesCited,
		"review_questions": verdict.ReviewQuestions,
		"iteration":        iteration,
		"max_iterations":   cap,
		"model":            usage.Model,
	}

	if verdict.Outcome == reviewer.VerdictAccepted {
		structuredPayload, _ := json.Marshal(map[string]any{
			"iteration":          iteration,
			"principles_cited":   verdict.PrinciplesCited,
			"proposed_contracts": proposed,
			"model":              usage.Model,
		})
		row, err := s.ledger.Append(ctx, ledger.LedgerEntry{
			WorkspaceID: st.WorkspaceID,
			ProjectID:   st.ProjectID,
			StoryID:     ledger.StringPtr(storyID),
			Type:        ledger.TypeDecision,
			Tags:        []string{planApprovedKind, planApprovedPhase},
			Content:     verdict.Rationale,
			Structured:  structuredPayload,
			CreatedBy:   caller.UserID,
		}, now)
		if err != nil {
			return mcpgo.NewToolResultError(fmt.Sprintf("write plan-approved row: %v", err)), nil
		}
		out["plan_approved_ledger_id"] = row.ID
	}

	body, _ := json.Marshal(out)
	s.logger.Info().
		Str("method", "tools/call").
		Str("tool", "orchestrator_submit_plan").
		Str("story_id", storyID).
		Str("verdict", verdict.Outcome).
		Int("iteration", iteration).
		Int("max", cap).
		Int64("duration_ms", time.Since(start).Milliseconds()).
		Msg("mcp tool call")
	return mcpgo.NewToolResultText(string(body)), nil
}

// resolvePlanReviewMaxIterations reads the plan-review iteration cap via
// the KV resolution chain (system → user → project → workspace). Defaults
// to defaultPlanReviewMaxIterations when no row resolves or when the
// resolved value is non-numeric.
func (s *Server) resolvePlanReviewMaxIterations(ctx context.Context, workspaceID, projectID, userID string) int {
	memberships := s.resolveCallerMembershipsForKV(ctx, userID)
	opts := ledger.KVResolveOptions{
		WorkspaceID: workspaceID,
		ProjectID:   projectID,
		UserID:      userID,
	}
	if opts.ProjectID != "" && opts.WorkspaceID == "" {
		opts.WorkspaceID = s.resolveProjectWorkspaceID(ctx, opts.ProjectID)
	}
	row, found, err := ledger.KVResolveScoped(ctx, s.ledger, planReviewMaxIterationsKey, opts, memberships)
	if err != nil || !found {
		return defaultPlanReviewMaxIterations
	}
	cap, parseErr := strconv.Atoi(strings.TrimSpace(row.Value))
	if parseErr != nil || cap < 1 {
		return defaultPlanReviewMaxIterations
	}
	return cap
}

// resolveCallerMembershipsForKV mirrors handleKVGetResolved's
// memberships handling — prepends the system sentinel so system-tier
// rows are visible.
func (s *Server) resolveCallerMembershipsForKV(ctx context.Context, userID string) []string {
	caller := CallerIdentity{UserID: userID}
	memberships := s.resolveCallerMemberships(ctx, caller)
	return append([]string{""}, memberships...)
}

// hasPlanApprovedRow reports whether a kind:plan-approved ledger row
// exists for the given story. Used by handleWorkflowClaim's precondition.
func (s *Server) hasPlanApprovedRow(ctx context.Context, projectID, storyID string, memberships []string) bool {
	rows, err := s.ledger.List(ctx, projectID, ledger.ListOptions{
		Type: ledger.TypeDecision,
		Tags: []string{planApprovedKind},
	}, memberships)
	if err != nil {
		return false
	}
	for _, r := range rows {
		if r.StoryID != nil && *r.StoryID == storyID && r.Status == ledger.StatusActive {
			return true
		}
	}
	return false
}
