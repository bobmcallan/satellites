package mcpserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/task"
)

func taskTestServer(t *testing.T) *Server {
	t.Helper()
	return &Server{
		cfg:    &config.Config{},
		tasks:  task.NewMemoryStore(),
		ledger: ledger.NewMemoryStore(),
	}
}

func callTaskHandler(t *testing.T, handler func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error), userID string, args map[string]any) *mcpgo.CallToolResult {
	t.Helper()
	ctx := context.WithValue(context.Background(), userKey, CallerIdentity{UserID: userID, Source: "apikey"})
	req := mcpgo.CallToolRequest{}
	req.Params.Arguments = args
	res, err := handler(ctx, req)
	require.NoError(t, err)
	return res
}

func TestTaskEnqueue_WritesRowAndLedgerRoot(t *testing.T) {
	t.Parallel()
	s := taskTestServer(t)
	res := callTaskHandler(t, s.handleTaskEnqueue, "apikey", map[string]any{
		"origin":       task.OriginScheduled,
		"workspace_id": "wksp_a",
		"priority":     task.PriorityHigh,
		"payload":      `{"job":"nightly"}`,
	})
	require.False(t, res.IsError, "enqueue: %+v", res)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(res.Content[0].(mcpgo.TextContent).Text), &out))
	assert.NotEmpty(t, out["task_id"])
	assert.NotEmpty(t, out["ledger_root_id"])
	assert.Equal(t, "enqueued", out["status"])
	assert.Equal(t, "high", out["priority"])

	// Verify task row exists via GetByID.
	taskID := out["task_id"].(string)
	row, err := s.tasks.GetByID(context.Background(), taskID, []string{"wksp_a"})
	require.NoError(t, err)
	assert.Equal(t, "scheduled", row.Origin)
}

func TestTaskClaim_ReturnsNullWhenEmpty(t *testing.T) {
	t.Parallel()
	s := taskTestServer(t)
	res := callTaskHandler(t, s.handleTaskClaim, "apikey", map[string]any{
		"worker_id":    "worker_a",
		"workspace_id": "wksp_a",
	})
	require.False(t, res.IsError)
	text := res.Content[0].(mcpgo.TextContent).Text
	assert.Equal(t, "null", text, "empty queue → null result")
}

func TestTaskClaim_PicksHighestPriorityFirst(t *testing.T) {
	t.Parallel()
	s := taskTestServer(t)
	// Enqueue medium first.
	callTaskHandler(t, s.handleTaskEnqueue, "apikey", map[string]any{
		"origin":       task.OriginScheduled,
		"workspace_id": "wksp_a",
		"priority":     task.PriorityMedium,
	})
	// Then critical.
	critRes := callTaskHandler(t, s.handleTaskEnqueue, "apikey", map[string]any{
		"origin":       task.OriginScheduled,
		"workspace_id": "wksp_a",
		"priority":     task.PriorityCritical,
	})
	var crit map[string]any
	require.NoError(t, json.Unmarshal([]byte(critRes.Content[0].(mcpgo.TextContent).Text), &crit))

	claimRes := callTaskHandler(t, s.handleTaskClaim, "apikey", map[string]any{
		"worker_id":    "worker_a",
		"workspace_id": "wksp_a",
	})
	require.False(t, claimRes.IsError)
	var claimed map[string]any
	require.NoError(t, json.Unmarshal([]byte(claimRes.Content[0].(mcpgo.TextContent).Text), &claimed))
	assert.Equal(t, crit["task_id"], claimed["id"], "critical should claim first even though medium was enqueued earlier")
	assert.Equal(t, "claimed", claimed["status"])
}

func TestTaskClose_SuccessfulTransition(t *testing.T) {
	t.Parallel()
	s := taskTestServer(t)
	enq := callTaskHandler(t, s.handleTaskEnqueue, "apikey", map[string]any{
		"origin":       task.OriginScheduled,
		"workspace_id": "wksp_a",
		"priority":     task.PriorityMedium,
	})
	var enqOut map[string]any
	require.NoError(t, json.Unmarshal([]byte(enq.Content[0].(mcpgo.TextContent).Text), &enqOut))
	taskID := enqOut["task_id"].(string)

	_ = callTaskHandler(t, s.handleTaskClaim, "apikey", map[string]any{
		"worker_id":    "worker_a",
		"workspace_id": "wksp_a",
	})

	closeRes := callTaskHandler(t, s.handleTaskClose, "apikey", map[string]any{
		"id":      taskID,
		"outcome": task.OutcomeSuccess,
	})
	require.False(t, closeRes.IsError, "close: %+v", closeRes)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(closeRes.Content[0].(mcpgo.TextContent).Text), &out))
	assert.Equal(t, "closed", out["status"])
	assert.Equal(t, "success", out["outcome"])
	// No stage hand-off because origin=scheduled.
	assert.Empty(t, out["handoff_task_id"])
}

func TestTaskGet_WorkspaceIsolation(t *testing.T) {
	t.Parallel()
	s := taskTestServer(t)
	enq := callTaskHandler(t, s.handleTaskEnqueue, "apikey", map[string]any{
		"origin":       task.OriginScheduled,
		"workspace_id": "wksp_a",
	})
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(enq.Content[0].(mcpgo.TextContent).Text), &out))
	taskID := out["task_id"].(string)

	// Caller's memberships default to empty-set since resolveCallerMemberships
	// in this test wiring returns nil for the fake caller — so GetByID with
	// memberships=nil lets it through. Instead, assert that supplying a
	// different workspace_id on the task doesn't leak into other workspaces.
	row, err := s.tasks.GetByID(context.Background(), taskID, []string{"wksp_b"})
	assert.ErrorIs(t, err, task.ErrNotFound)
	assert.Empty(t, row.ID)
}

func TestTaskList_FiltersByStatus(t *testing.T) {
	t.Parallel()
	s := taskTestServer(t)
	// Enqueue two tasks, claim one.
	for range []int{0, 1} {
		callTaskHandler(t, s.handleTaskEnqueue, "apikey", map[string]any{
			"origin":       task.OriginScheduled,
			"workspace_id": "wksp_a",
		})
	}
	_ = callTaskHandler(t, s.handleTaskClaim, "apikey", map[string]any{
		"worker_id":    "worker_a",
		"workspace_id": "wksp_a",
	})

	listRes := callTaskHandler(t, s.handleTaskList, "apikey", map[string]any{
		"status": task.StatusEnqueued,
	})
	require.False(t, listRes.IsError)
	var rows []map[string]any
	require.NoError(t, json.Unmarshal([]byte(listRes.Content[0].(mcpgo.TextContent).Text), &rows))
	// 1 enqueued + 1 claimed; filter asks for enqueued.
	assert.Len(t, rows, 1)
	for _, row := range rows {
		assert.Equal(t, "enqueued", row["status"])
	}
}

// TestTaskClose_LedgerRowWritten verifies the handler writes the
// kind:task-closed ledger row per AC 5.
func TestTaskClose_LedgerRowWritten(t *testing.T) {
	t.Parallel()
	s := taskTestServer(t)
	enq := callTaskHandler(t, s.handleTaskEnqueue, "apikey", map[string]any{
		"origin":       task.OriginScheduled,
		"workspace_id": "wksp_a",
	})
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(enq.Content[0].(mcpgo.TextContent).Text), &out))
	taskID := out["task_id"].(string)

	_ = callTaskHandler(t, s.handleTaskClose, "apikey", map[string]any{
		"id":      taskID,
		"outcome": task.OutcomeSuccess,
	})

	rows, err := s.ledger.List(context.Background(), "", ledger.ListOptions{}, nil)
	require.NoError(t, err)
	foundClosed := false
	for _, r := range rows {
		for _, tag := range r.Tags {
			if tag == "kind:task-closed" {
				foundClosed = true
			}
		}
	}
	_ = time.Now()
	assert.True(t, foundClosed, "expected kind:task-closed ledger row")
}
