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

// handleTaskEnqueue writes a task at the legacy status=enqueued. New
// callers should use task_plan (drafting) or task_publish (committing)
// explicitly per sty_c1200f75. Subscribers see enqueued and published
// rows alike via task.SubscriberVisibleStatuses.
func (s *Server) handleTaskEnqueue(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	return s.createTask(ctx, req, task.StatusEnqueued, "task_enqueue")
}

// handleTaskPlan implements task_plan: write a task at status=planned —
// the agent's drafting state. Subscribers do not see planned rows.
// sty_c1200f75.
func (s *Server) handleTaskPlan(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	return s.createTask(ctx, req, task.StatusPlanned, "task_plan")
}

// handleTaskPublish implements task_publish. Two modes:
//   - With task_id: flips an existing planned task to published.
//   - Without task_id: same args as task_plan; creates and publishes
//     in one call (skips the planned step). sty_c1200f75.
func (s *Server) handleTaskPublish(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	if s.tasks == nil {
		return mcpgo.NewToolResultError("task_publish unavailable: task store not configured"), nil
	}
	args := req.GetArguments()
	if id := getString(args, "task_id"); id != "" {
		caller, _ := UserFrom(ctx)
		memberships := s.resolveCallerMemberships(ctx, caller)
		t, err := s.tasks.Publish(ctx, id, time.Now().UTC(), memberships)
		if err != nil {
			return mcpgo.NewToolResultError(fmt.Sprintf("task_publish: %s", err)), nil
		}
		if s.ledger != nil {
			_, _ = s.ledger.Append(ctx, ledger.LedgerEntry{
				WorkspaceID: t.WorkspaceID,
				ProjectID:   t.ProjectID,
				Type:        ledger.TypeDecision,
				Tags:        []string{"kind:task-published", "task_id:" + t.ID},
				Content:     fmt.Sprintf("task published: id=%s", t.ID),
				Durability:  ledger.DurabilityDurable,
				SourceType:  ledger.SourceAgent,
				Status:      ledger.StatusActive,
				CreatedBy:   caller.UserID,
			}, time.Now().UTC())
		}
		return jsonResult(map[string]any{
			"task_id":      t.ID,
			"workspace_id": t.WorkspaceID,
			"status":       t.Status,
		})
	}
	return s.createTask(ctx, req, task.StatusPublished, "task_publish")
}

// createTask is the shared body of task_enqueue / task_plan /
// task_publish (no-id form). status names the row's initial state.
func (s *Server) createTask(ctx context.Context, req mcpgo.CallToolRequest, status, verbName string) (*mcpgo.CallToolResult, error) {
	if s.tasks == nil {
		return mcpgo.NewToolResultError(verbName + " unavailable: task store not configured"), nil
	}
	caller, _ := UserFrom(ctx)
	memberships := s.resolveCallerMemberships(ctx, caller)
	args := req.GetArguments()
	origin := getString(args, "origin")
	if origin == "" {
		return mcpgo.NewToolResultError(verbName + " requires origin"), nil
	}
	workspaceID := getString(args, "workspace_id")
	if workspaceID == "" {
		if len(memberships) == 0 {
			return mcpgo.NewToolResultError(verbName + ": no caller workspace memberships"), nil
		}
		workspaceID = memberships[0]
	}
	priority := getString(args, "priority")
	if priority == "" {
		priority = task.PriorityMedium
	}
	projectID := getString(args, "project_id")
	contractInstanceID := getString(args, "contract_instance_id")
	kind := getString(args, "kind")
	agentID := getString(args, "agent_id")
	parentTaskID := getString(args, "parent_task_id")
	priorTaskID := getString(args, "prior_task_id")
	triggerRaw := []byte(getString(args, "trigger"))
	payloadRaw := []byte(getString(args, "payload"))
	expectedStr := getString(args, "expected_duration")
	var expected time.Duration
	if expectedStr != "" {
		if d, err := time.ParseDuration(expectedStr); err == nil {
			expected = d
		}
	}
	// sty_c6d76a5b: when parent_task_id resolves to a known task and the
	// caller didn't override, inherit the parent's contract_instance_id /
	// project_id / agent_id so successor tasks form a coherent thread
	// without the caller restating each field.
	if parentTaskID != "" && s.tasks != nil {
		if parent, perr := s.tasks.GetByID(ctx, parentTaskID, memberships); perr == nil {
			if contractInstanceID == "" {
				contractInstanceID = parent.ContractInstanceID
			}
			if projectID == "" {
				projectID = parent.ProjectID
			}
			if agentID == "" {
				agentID = parent.AgentID
			}
		}
	}
	now := time.Now().UTC()
	seed := s.stampTaskIteration(ctx, task.Task{
		WorkspaceID:        workspaceID,
		ProjectID:          projectID,
		ContractInstanceID: contractInstanceID,
		Kind:               kind,
		AgentID:            agentID,
		ParentTaskID:       parentTaskID,
		PriorTaskID:        priorTaskID,
		Status:             status,
		Origin:             origin,
		Trigger:            triggerRaw,
		Payload:            payloadRaw,
		Priority:           priority,
		ExpectedDuration:   expected,
	}, memberships)
	t, err := s.tasks.Enqueue(ctx, seed, now)
	if err != nil {
		return mcpgo.NewToolResultError(fmt.Sprintf("%s: %s", verbName, err)), nil
	}
	ledgerID := ""
	if s.ledger != nil {
		ledgerKind := "kind:task-published"
		if t.Status == task.StatusPlanned {
			ledgerKind = "kind:task-planned"
		} else if t.Status == task.StatusEnqueued {
			ledgerKind = "kind:task-enqueued"
		}
		row, lerr := s.ledger.Append(ctx, ledger.LedgerEntry{
			WorkspaceID: t.WorkspaceID,
			ProjectID:   t.ProjectID,
			Type:        ledger.TypeDecision,
			Tags: []string{
				ledgerKind,
				"task_id:" + t.ID,
				"origin:" + t.Origin,
				"priority:" + t.Priority,
			},
			Content:    fmt.Sprintf("task created: id=%s status=%s origin=%s priority=%s", t.ID, t.Status, t.Origin, t.Priority),
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
		Origin:             getString(args, "origin"),
		Status:             getString(args, "status"),
		Priority:           getString(args, "priority"),
		ClaimedBy:          getString(args, "claimed_by"),
		ContractInstanceID: getString(args, "contract_instance_id"),
		Kind:               getString(args, "kind"),
	}
	if v, ok := args["include_archived"].(bool); ok {
		opts.IncludeArchived = v
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

// successorTaskSpec is the shape each entry in task_close's
// successor_tasks JSON array must satisfy. Mirrors the additive args
// task_enqueue accepts (origin/kind/agent_id/parent_task_id/etc.). The
// substrate stamps parent_task_id to the closing task's id when the
// caller leaves it empty so the successor stays anchored to the
// conversation thread by default. sty_c6d76a5b.
type successorTaskSpec struct {
	Origin             string `json:"origin"`
	Kind               string `json:"kind,omitempty"`
	AgentID            string `json:"agent_id,omitempty"`
	ParentTaskID       string `json:"parent_task_id,omitempty"`
	PriorTaskID        string `json:"prior_task_id,omitempty"`
	ContractInstanceID string `json:"contract_instance_id,omitempty"`
	ProjectID          string `json:"project_id,omitempty"`
	WorkspaceID        string `json:"workspace_id,omitempty"`
	Priority           string `json:"priority,omitempty"`
	Payload            string `json:"payload,omitempty"`
	Trigger            string `json:"trigger,omitempty"`
	ExpectedDuration   string `json:"expected_duration,omitempty"`
	Status             string `json:"status,omitempty"` // planned | published | enqueued (default published)
}

// handleTaskClose implements task_close: transition + kind:task-closed
// ledger row + stage hand-off when origin=story_stage and outcome=success.
//
// sty_c6d76a5b: optional successor_tasks JSON array — when supplied, the
// substrate creates each successor immediately after the close,
// stamping parent_task_id to the closing task's id by default so the
// emitted thread anchors back to the conversation. successor_tasks may
// be a JSON-encoded string or a native array, depending on the caller's
// MCP transport.
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
	successors, serr := decodeSuccessorTasks(args["successor_tasks"])
	if serr != nil {
		body, _ := json.Marshal(map[string]any{
			"error":   "successor_tasks_invalid",
			"message": serr.Error(),
		})
		return mcpgo.NewToolResultError(string(body)), nil
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

	successorIDs, sErr := s.emitSuccessorTasks(ctx, closed, successors, caller.UserID, now, memberships)
	if sErr != nil {
		// Successor creation failure does not roll back the close —
		// closing is already recorded. Surface the partial outcome so
		// the caller can retry the missing successors.
		body, _ := json.Marshal(map[string]any{
			"task_id":           closed.ID,
			"status":            closed.Status,
			"outcome":           closed.Outcome,
			"completed_at":      closed.CompletedAt,
			"handoff_task_id":   handoffTaskID,
			"successor_task_ids": successorIDs,
			"successor_error":   sErr.Error(),
		})
		return mcpgo.NewToolResultError(string(body)), nil
	}

	return jsonResult(map[string]any{
		"task_id":            closed.ID,
		"status":             closed.Status,
		"outcome":            closed.Outcome,
		"completed_at":       closed.CompletedAt,
		"handoff_task_id":    handoffTaskID,
		"successor_task_ids": successorIDs,
	})
}

// decodeSuccessorTasks accepts either a JSON-encoded string or a native
// []any (depending on MCP transport) and returns the parsed slice.
// Empty input returns nil with no error.
func decodeSuccessorTasks(raw any) ([]successorTaskSpec, error) {
	if raw == nil {
		return nil, nil
	}
	switch v := raw.(type) {
	case string:
		if v == "" {
			return nil, nil
		}
		var out []successorTaskSpec
		if err := json.Unmarshal([]byte(v), &out); err != nil {
			return nil, fmt.Errorf("parse successor_tasks JSON: %w", err)
		}
		return out, nil
	case []any:
		if len(v) == 0 {
			return nil, nil
		}
		buf, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("re-encode successor_tasks: %w", err)
		}
		var out []successorTaskSpec
		if err := json.Unmarshal(buf, &out); err != nil {
			return nil, fmt.Errorf("decode successor_tasks: %w", err)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("successor_tasks must be a JSON array or JSON string, got %T", raw)
	}
}

// emitSuccessorTasks creates each spec as a fresh task, defaulting
// parent_task_id to closed.ID and inheriting workspace / project /
// contract_instance / agent from the closing task when the spec leaves
// them empty. Returns the created task ids in order.
func (s *Server) emitSuccessorTasks(
	ctx context.Context,
	closed task.Task,
	specs []successorTaskSpec,
	actor string,
	now time.Time,
	memberships []string,
) ([]string, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(specs))
	for i, spec := range specs {
		origin := spec.Origin
		if origin == "" {
			origin = task.OriginStoryStage
		}
		status := spec.Status
		if status == "" {
			status = task.StatusPublished
		}
		workspaceID := spec.WorkspaceID
		if workspaceID == "" {
			workspaceID = closed.WorkspaceID
		}
		projectID := spec.ProjectID
		if projectID == "" {
			projectID = closed.ProjectID
		}
		contractID := spec.ContractInstanceID
		if contractID == "" {
			contractID = closed.ContractInstanceID
		}
		agentID := spec.AgentID
		if agentID == "" {
			agentID = closed.AgentID
		}
		parentID := spec.ParentTaskID
		if parentID == "" {
			parentID = closed.ID
		}
		priority := spec.Priority
		if priority == "" {
			priority = task.PriorityMedium
		}
		var expected time.Duration
		if spec.ExpectedDuration != "" {
			if d, perr := time.ParseDuration(spec.ExpectedDuration); perr == nil {
				expected = d
			}
		}
		seed := s.stampTaskIteration(ctx, task.Task{
			WorkspaceID:        workspaceID,
			ProjectID:          projectID,
			ContractInstanceID: contractID,
			Kind:               spec.Kind,
			AgentID:            agentID,
			ParentTaskID:       parentID,
			PriorTaskID:        spec.PriorTaskID,
			Status:             status,
			Origin:             origin,
			Trigger:            []byte(spec.Trigger),
			Payload:            []byte(spec.Payload),
			Priority:           priority,
			ExpectedDuration:   expected,
		}, memberships)
		t, err := s.tasks.Enqueue(ctx, seed, now)
		if err != nil {
			return out, fmt.Errorf("successor[%d]: %w", i, err)
		}
		out = append(out, t.ID)
		if s.ledger != nil {
			_, _ = s.ledger.Append(ctx, ledger.LedgerEntry{
				WorkspaceID: t.WorkspaceID,
				ProjectID:   t.ProjectID,
				Type:        ledger.TypeDecision,
				Tags: []string{
					"kind:task-successor",
					"task_id:" + t.ID,
					"parent_task_id:" + parentID,
				},
				Content:    fmt.Sprintf("successor task spawned: parent=%s child=%s kind=%s", parentID, t.ID, spec.Kind),
				Structured: t.Payload,
				Durability: ledger.DurabilityDurable,
				SourceType: ledger.SourceAgent,
				Status:     ledger.StatusActive,
				CreatedBy:  actor,
			}, now)
		}
	}
	return out, nil
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
