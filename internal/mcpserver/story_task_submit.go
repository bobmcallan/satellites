// Package mcpserver — story_task_submit MCP verb (sty_c6d76a5b).
//
// The agent-authored, substrate-validated alternative to
// orchestrator_compose_plan. The orchestrator submits the full task
// list; the substrate validates structural invariants (plan first,
// review tasks present per work task, contract names known) and
// rejects submissions that violate them — it does not silently
// mutate.
//
// Initial slice covers `kind=plan` only. `kind=close` and
// `kind=spawn` modes are scoped for follow-up commits per the
// story body.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/story"
	"github.com/bobmcallan/satellites/internal/task"
)

// SubmitKindPlan is the verb-arg value for an initial plan submission.
// The verb arg `kind` and Task.Kind are distinct enums — `kind` on the
// verb names the submission shape (plan | close | spawn), while
// task.Kind on each list entry names the activity type (work | review).
const SubmitKindPlan = "plan"

// taskInput is the JSON shape of one entry in the `tasks[]` arg of
// story_task_submit. The agent supplies kind, action, description,
// and (optionally) agent_id; the substrate fills in the rest.
type taskInput struct {
	Kind        string `json:"kind"`
	Action      string `json:"action"`
	Description string `json:"description,omitempty"`
	AgentID     string `json:"agent_id,omitempty"`
	Priority    string `json:"priority,omitempty"`
}

// handleStoryTaskSubmit implements `story_task_submit`. The first
// supported mode is `kind=plan`: the orchestrator submits an
// agent-authored task list; the substrate validates and persists.
func (s *Server) handleStoryTaskSubmit(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	start := time.Now()
	caller, _ := UserFrom(ctx)

	storyID, err := req.RequireString("story_id")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	kind, err := req.RequireString("kind")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}

	if s.stories == nil || s.tasks == nil || s.ledger == nil {
		return mcpgo.NewToolResultError("story_task_submit unavailable: required stores not configured"), nil
	}

	memberships := s.resolveCallerMemberships(ctx, caller)
	st, err := s.stories.GetByID(ctx, storyID, memberships)
	if err != nil {
		return mcpgo.NewToolResultError("story not found"), nil
	}

	switch kind {
	case SubmitKindPlan:
		return s.submitPlan(ctx, req, st, caller, memberships, start)
	default:
		return mcpgo.NewToolResultError(fmt.Sprintf("story_task_submit: unsupported kind %q (only %q is implemented in this slice)", kind, SubmitKindPlan)), nil
	}
}

// submitPlan handles `kind=plan` — the orchestrator's agent-authored
// initial task list. Validates structure, persists the tasks, writes
// a kind:plan ledger row, and returns the new task ids.
func (s *Server) submitPlan(ctx context.Context, req mcpgo.CallToolRequest, st story.Story, caller CallerIdentity, memberships []string, start time.Time) (*mcpgo.CallToolResult, error) {
	rawTasks := req.GetString("tasks", "")
	if strings.TrimSpace(rawTasks) == "" {
		return mcpgo.NewToolResultError("story_task_submit(kind=plan): tasks[] required"), nil
	}
	var inputs []taskInput
	if err := json.Unmarshal([]byte(rawTasks), &inputs); err != nil {
		return mcpgo.NewToolResultError(fmt.Sprintf("story_task_submit: tasks[] parse error: %v", err)), nil
	}
	if len(inputs) == 0 {
		return mcpgo.NewToolResultError("story_task_submit(kind=plan): tasks[] empty"), nil
	}

	// Idempotence: when the story already has tasks submitted via
	// story_task_submit, return the existing task ids without writing
	// fresh rows. Mirrors orchestrator_compose_plan's convention.
	existing, _ := s.tasks.List(ctx, task.ListOptions{StoryID: st.ID}, memberships)
	if len(existing) > 0 {
		ids := make([]string, len(existing))
		for i, t := range existing {
			ids[i] = t.ID
		}
		body, _ := json.Marshal(map[string]any{
			"story_id":   st.ID,
			"task_ids":   ids,
			"task_count": len(ids),
			"idempotent": true,
		})
		return mcpgo.NewToolResultText(string(body)), nil
	}

	if err := validatePlanTasks(inputs); err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}

	now := s.nowUTC()
	planMarkdown := req.GetString("plan_markdown", "")

	// Persist tasks. Status=published — claimable now. The status of
	// review tasks under sty_c6d76a5b is `planned` until the
	// matching work task closes; that gating moves to story_task_submit
	// (kind=close) in a follow-up slice. For this slice all submitted
	// tasks land published so the existing reviewer service can claim
	// review tasks once the work task's close-handler publishes them.
	taskIDs := make([]string, 0, len(inputs))
	for i, in := range inputs {
		priority := in.Priority
		if priority == "" {
			priority = task.PriorityMedium
		}
		t, err := s.tasks.Enqueue(ctx, task.Task{
			WorkspaceID: st.WorkspaceID,
			ProjectID:   st.ProjectID,
			StoryID:     st.ID,
			Kind:        in.Kind,
			Action:      in.Action,
			Description: in.Description,
			AgentID:     in.AgentID,
			Origin:      task.OriginStoryStage,
			Priority:    priority,
			Status:      task.StatusPublished,
		}, now)
		if err != nil {
			return mcpgo.NewToolResultError(fmt.Sprintf("task enqueue [%d %s]: %v", i, in.Action, err)), nil
		}
		taskIDs = append(taskIDs, t.ID)
	}

	// kind:plan ledger row carrying the plan_markdown. The plan
	// markdown is the artifact; the task list is the conversation
	// scaffold.
	planPayload, _ := json.Marshal(map[string]any{
		"task_ids": taskIDs,
		"actions":  collectActions(inputs),
	})
	planContent := planMarkdown
	if planContent == "" {
		planContent = fmt.Sprintf("orchestrator plan: %s", strings.Join(collectActions(inputs), " → "))
	}
	planRow, err := s.ledger.Append(ctx, ledger.LedgerEntry{
		WorkspaceID: st.WorkspaceID,
		ProjectID:   st.ProjectID,
		StoryID:     ledger.StringPtr(st.ID),
		Type:        ledger.TypePlan,
		Tags:        []string{"kind:plan", "phase:orchestrator", "story:" + st.ID, "source:story_task_submit"},
		Content:     planContent,
		Structured:  planPayload,
		CreatedBy:   caller.UserID,
	}, now)
	if err != nil {
		return mcpgo.NewToolResultError(fmt.Sprintf("plan ledger append: %v", err)), nil
	}

	body, _ := json.Marshal(map[string]any{
		"story_id":       st.ID,
		"plan_ledger_id": planRow.ID,
		"task_ids":       taskIDs,
		"task_count":     len(taskIDs),
	})
	s.logger.Info().
		Str("method", "tools/call").
		Str("tool", "story_task_submit").
		Str("kind", SubmitKindPlan).
		Str("story_id", st.ID).
		Int("task_count", len(taskIDs)).
		Int64("duration_ms", time.Since(start).Milliseconds()).
		Msg("mcp tool call")
	return mcpgo.NewToolResultText(string(body)), nil
}

// validatePlanTasks runs the structural checks required by
// `story_task_submit(kind=plan)` per sty_c6d76a5b's AC list. Returns
// the first violation as a structured error string; nil when clean.
//
// Structural rules in this slice:
//   1. tasks[] non-empty.
//   2. tasks[0].action == "contract:plan".
//   3. tasks[0].kind == KindWork (the plan task itself is work).
//   4. Every kind=work task has an immediate kind=review sibling
//      in the next slot — substrate does not silently insert.
//   5. Every action is well-formed (contract:<name> with a non-empty
//      contract name).
//
// Deferred to a follow-up slice (require contract markdown
// enumeration / agent capability lookups):
//   - Last task action == "contract:story_close".
//   - Required contract set fully covered.
//   - Each task's agent_id has the capability to deliver/review.
func validatePlanTasks(inputs []taskInput) error {
	if len(inputs) == 0 {
		return fmt.Errorf("plan_tasks_empty: tasks[] must not be empty")
	}
	first := inputs[0]
	firstKind := first.Kind
	if firstKind == "" {
		firstKind = task.KindWork
	}
	if first.Action != task.ContractAction("plan") {
		return fmt.Errorf("plan_first_task_must_be_plan: tasks[0].action = %q (expected %q)", first.Action, task.ContractAction("plan"))
	}
	if firstKind != task.KindWork {
		return fmt.Errorf("plan_first_task_must_be_work: tasks[0].kind = %q (expected %q)", firstKind, task.KindWork)
	}

	for i, in := range inputs {
		if in.Action == "" {
			return fmt.Errorf("task_action_required: tasks[%d].action is empty", i)
		}
		if task.ContractFromAction(in.Action) == "" {
			return fmt.Errorf("invalid_action_format: tasks[%d].action = %q (expected %q<name>)", i, in.Action, task.ActionContractPrefix)
		}
		kind := in.Kind
		if kind == "" {
			kind = task.KindWork
		}
		if kind != task.KindWork && kind != task.KindReview {
			return fmt.Errorf("invalid_task_kind: tasks[%d].kind = %q (expected %q or %q)", i, in.Kind, task.KindWork, task.KindReview)
		}
		if kind != task.KindWork {
			continue
		}
		// Every work task needs an immediate review sibling matching
		// the same action. The agent must include it explicitly —
		// substrate does not insert.
		if i+1 >= len(inputs) {
			return fmt.Errorf("missing_review_for: tasks[%d] action=%q has no review sibling (tasks[] ended)", i, in.Action)
		}
		next := inputs[i+1]
		if next.Kind != task.KindReview {
			return fmt.Errorf("missing_review_for: tasks[%d+1] kind=%q (expected %q for action=%q)", i, next.Kind, task.KindReview, in.Action)
		}
		if next.Action != in.Action {
			return fmt.Errorf("review_action_mismatch: tasks[%d+1] action=%q does not match work action=%q", i, next.Action, in.Action)
		}
	}
	return nil
}

// collectActions returns the action strings for each input task, in
// order. Used in the plan ledger row's structured payload + content
// summary.
func collectActions(inputs []taskInput) []string {
	out := make([]string, len(inputs))
	for i, in := range inputs {
		out[i] = in.Action
	}
	return out
}

