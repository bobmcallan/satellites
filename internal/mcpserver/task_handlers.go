package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/task"
)

// handleTaskEnqueue implements task_enqueue: create a new enqueued task
// + write a kind:task-enqueued ledger row scoped to the caller's
// workspace.
func (s *Server) handleTaskEnqueue(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	if s.tasks == nil {
		return mcpgo.NewToolResultError("task_enqueue unavailable: task store not configured"), nil
	}
	caller, _ := UserFrom(ctx)
	memberships := s.resolveCallerMemberships(ctx, caller)
	args := req.GetArguments()
	origin := getString(args, "origin")
	if origin == "" {
		return mcpgo.NewToolResultError("task_enqueue requires origin"), nil
	}
	workspaceID := getString(args, "workspace_id")
	if workspaceID == "" {
		if len(memberships) == 0 {
			return mcpgo.NewToolResultError("task_enqueue: no caller workspace memberships"), nil
		}
		workspaceID = memberships[0]
	}
	priority := getString(args, "priority")
	if priority == "" {
		priority = task.PriorityMedium
	}
	projectID := getString(args, "project_id")
	triggerRaw := []byte(getString(args, "trigger"))
	payloadRaw := []byte(getString(args, "payload"))
	expectedStr := getString(args, "expected_duration")
	var expected time.Duration
	if expectedStr != "" {
		if d, err := time.ParseDuration(expectedStr); err == nil {
			expected = d
		}
	}
	now := time.Now().UTC()
	t, err := s.tasks.Enqueue(ctx, task.Task{
		WorkspaceID:      workspaceID,
		ProjectID:        projectID,
		Origin:           origin,
		Trigger:          triggerRaw,
		Payload:          payloadRaw,
		Priority:         priority,
		ExpectedDuration: expected,
	}, now)
	if err != nil {
		return mcpgo.NewToolResultError(fmt.Sprintf("task_enqueue: %s", err)), nil
	}
	ledgerID := ""
	if s.ledger != nil {
		row, lerr := s.ledger.Append(ctx, ledger.LedgerEntry{
			WorkspaceID: t.WorkspaceID,
			ProjectID:   t.ProjectID,
			Type:        ledger.TypeDecision,
			Tags: []string{
				"kind:task-enqueued",
				"task_id:" + t.ID,
				"origin:" + t.Origin,
				"priority:" + t.Priority,
			},
			Content:    fmt.Sprintf("task enqueued: id=%s origin=%s priority=%s", t.ID, t.Origin, t.Priority),
			Structured: t.Payload,
			Durability: ledger.DurabilityDurable,
			SourceType: ledger.SourceAgent,
			Status:     ledger.StatusActive,
			CreatedBy:  caller.UserID,
		}, now)
		if lerr == nil {
			ledgerID = row.ID
			t.LedgerRootID = row.ID
		}
	}
	return jsonResult(map[string]any{
		"task_id":        t.ID,
		"ledger_root_id": ledgerID,
		"workspace_id":   t.WorkspaceID,
		"status":         t.Status,
		"priority":       t.Priority,
		"origin":         t.Origin,
	})
}

// handleTaskGet implements task_get with workspace scoping.
func (s *Server) handleTaskGet(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	if s.tasks == nil {
		return mcpgo.NewToolResultError("task_get unavailable"), nil
	}
	caller, _ := UserFrom(ctx)
	memberships := s.resolveCallerMemberships(ctx, caller)
	id, err := req.RequireString("id")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	t, err := s.tasks.GetByID(ctx, id, memberships)
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	return jsonResult(t)
}

// handleTaskList implements task_list.
func (s *Server) handleTaskList(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	if s.tasks == nil {
		return mcpgo.NewToolResultError("task_list unavailable"), nil
	}
	caller, _ := UserFrom(ctx)
	memberships := s.resolveCallerMemberships(ctx, caller)
	args := req.GetArguments()
	opts := task.ListOptions{
		Origin:    getString(args, "origin"),
		Status:    getString(args, "status"),
		Priority:  getString(args, "priority"),
		ClaimedBy: getString(args, "claimed_by"),
	}
	if v, ok := args["limit"].(float64); ok {
		opts.Limit = int(v)
	}
	rows, err := s.tasks.List(ctx, opts, memberships)
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	return jsonResult(rows)
}

// handleTaskClaim implements task_claim: atomic pick + kind:task-claimed
// ledger row. Returns null result when queue is empty (not an error).
func (s *Server) handleTaskClaim(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	if s.tasks == nil {
		return mcpgo.NewToolResultError("task_claim unavailable"), nil
	}
	caller, _ := UserFrom(ctx)
	memberships := s.resolveCallerMemberships(ctx, caller)
	args := req.GetArguments()
	workerID := getString(args, "worker_id")
	if workerID == "" {
		workerID = caller.UserID
	}
	if workerID == "" {
		return mcpgo.NewToolResultError("task_claim requires worker_id"), nil
	}
	workspaceIDs := memberships
	if scoped := getString(args, "workspace_id"); scoped != "" {
		workspaceIDs = []string{scoped}
	}
	now := time.Now().UTC()
	t, err := s.tasks.Claim(ctx, workerID, workspaceIDs, now)
	if errors.Is(err, task.ErrNoTaskAvailable) {
		return jsonResult(nil)
	}
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	if s.ledger != nil {
		_, _ = s.ledger.Append(ctx, ledger.LedgerEntry{
			WorkspaceID: t.WorkspaceID,
			ProjectID:   t.ProjectID,
			Type:        ledger.TypeDecision,
			Tags: []string{
				"kind:task-claimed",
				"task_id:" + t.ID,
				"worker_id:" + workerID,
			},
			Content:    fmt.Sprintf("task claimed: id=%s worker=%s", t.ID, workerID),
			Durability: ledger.DurabilityDurable,
			SourceType: ledger.SourceAgent,
			Status:     ledger.StatusActive,
			CreatedBy:  caller.UserID,
		}, now)
	}
	return jsonResult(t)
}

// handleTaskClose implements task_close: transition + kind:task-closed
// ledger row + stage hand-off when origin=story_stage and outcome=success.
func (s *Server) handleTaskClose(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	if s.tasks == nil {
		return mcpgo.NewToolResultError("task_close unavailable"), nil
	}
	caller, _ := UserFrom(ctx)
	memberships := s.resolveCallerMemberships(ctx, caller)
	args := req.GetArguments()
	id, err := req.RequireString("id")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	outcome, err := req.RequireString("outcome")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	// Stale-claim rejection: when the caller supplies worker_id, verify
	// the task's current ClaimedBy matches. A mismatch means the task
	// was reclaimed by the watchdog (and possibly re-picked by another
	// worker) between claim and close. Story_b4513c8c.
	workerID := getString(args, "worker_id")
	if workerID != "" {
		existing, err := s.tasks.GetByID(ctx, id, memberships)
		if err == nil && existing.ClaimedBy != "" && existing.ClaimedBy != workerID {
			body, _ := json.Marshal(map[string]any{
				"error":           "stale_claim",
				"task_id":         id,
				"current_claimer": existing.ClaimedBy,
				"caller":          workerID,
				"reclaim_count":   existing.ReclaimCount,
			})
			return mcpgo.NewToolResultError(string(body)), nil
		}
	}
	now := time.Now().UTC()
	closed, err := s.tasks.Close(ctx, id, outcome, now, memberships)
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	if s.ledger != nil {
		_, _ = s.ledger.Append(ctx, ledger.LedgerEntry{
			WorkspaceID: closed.WorkspaceID,
			ProjectID:   closed.ProjectID,
			Type:        ledger.TypeDecision,
			Tags: []string{
				"kind:task-closed",
				"task_id:" + closed.ID,
				"outcome:" + outcome,
			},
			Content:    fmt.Sprintf("task closed: id=%s outcome=%s", closed.ID, outcome),
			Durability: ledger.DurabilityDurable,
			SourceType: ledger.SourceAgent,
			Status:     ledger.StatusActive,
			CreatedBy:  caller.UserID,
		}, now)
	}

	handoffTaskID := ""
	if outcome == task.OutcomeSuccess && closed.Origin == task.OriginStoryStage {
		handoffTaskID = s.enqueueStageHandoff(ctx, closed, caller.UserID, now, memberships)
	}

	return jsonResult(map[string]any{
		"task_id":             closed.ID,
		"status":              closed.Status,
		"outcome":             closed.Outcome,
		"completed_at":        closed.CompletedAt,
		"handoff_task_id":     handoffTaskID,
	})
}

// enqueueStageHandoff: when a story_stage task closes successfully and
// the parent story has another CI in status=ready, enqueue a task for
// it. Best-effort — logged on failure, does not roll back the close.
func (s *Server) enqueueStageHandoff(ctx context.Context, closed task.Task, actor string, now time.Time, memberships []string) string {
	if s.contracts == nil || len(closed.Payload) == 0 {
		return ""
	}
	var payload struct {
		ContractInstanceID string `json:"contract_instance_id"`
		StoryID            string `json:"story_id"`
	}
	if err := json.Unmarshal(closed.Payload, &payload); err != nil || payload.StoryID == "" {
		return ""
	}
	cis, err := s.contracts.List(ctx, payload.StoryID, memberships)
	if err != nil {
		return ""
	}
	var next *contract.ContractInstance
	for i := range cis {
		if cis[i].Status == contract.StatusReady {
			next = &cis[i]
			break
		}
	}
	if next == nil {
		return ""
	}
	triggerBytes, _ := json.Marshal(map[string]any{"prior_task_id": closed.ID})
	payloadBytes, _ := json.Marshal(map[string]any{
		"contract_instance_id": next.ID,
		"story_id":             next.StoryID,
	})
	// Derive the hand-off task's priority from the parent story per
	// §4 dispatch rule 2 ("Priority is the story's priority for
	// origin=story_stage tasks"). Falls through to medium when the
	// story can't be resolved or carries a non-canonical priority.
	priority := task.PriorityMedium
	if s.stories != nil {
		if story, err := s.stories.GetByID(ctx, next.StoryID, memberships); err == nil {
			switch story.Priority {
			case task.PriorityCritical, task.PriorityHigh, task.PriorityMedium, task.PriorityLow:
				priority = story.Priority
			}
		}
	}
	handoff, err := s.tasks.Enqueue(ctx, task.Task{
		WorkspaceID: next.WorkspaceID,
		ProjectID:   next.ProjectID,
		Origin:      task.OriginStoryStage,
		Trigger:     triggerBytes,
		Payload:     payloadBytes,
		Priority:    priority,
	}, now)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn().Str("error", err.Error()).Str("closed_task_id", closed.ID).Msg("stage hand-off enqueue failed")
		}
		return ""
	}
	if s.ledger != nil {
		_, _ = s.ledger.Append(ctx, ledger.LedgerEntry{
			WorkspaceID: handoff.WorkspaceID,
			ProjectID:   handoff.ProjectID,
			Type:        ledger.TypeDecision,
			Tags: []string{
				"kind:task-enqueued",
				"task_id:" + handoff.ID,
				"trigger:stage-handoff",
				"prior_task_id:" + closed.ID,
				"ci_id:" + next.ID,
			},
			Content:    fmt.Sprintf("stage hand-off: from task=%s to ci=%s", closed.ID, next.ID),
			Durability: ledger.DurabilityDurable,
			SourceType: ledger.SourceSystem,
			Status:     ledger.StatusActive,
			CreatedBy:  actor,
		}, now)
	}
	return handoff.ID
}
