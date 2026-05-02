package mcpserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	satarbor "github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/story"
	"github.com/bobmcallan/satellites/internal/task"
	"github.com/bobmcallan/satellites/internal/workspace"
)

// taskWalkFixture wires the substrate stores task_walk reads so the
// handler can compose a realistic payload.
type taskWalkFixture struct {
	t          *testing.T
	server     *Server
	caller     CallerIdentity
	wsID       string
	projectID  string
	storyID    string
	developCIs []contract.ContractInstance
	pushCI     contract.ContractInstance
	now        time.Time
}

func newTaskWalkFixture(t *testing.T) *taskWalkFixture {
	t.Helper()
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	cfg := &config.Config{}
	wsStore := workspace.NewMemoryStore()
	docStore := document.NewMemoryStore()
	ledStore := ledger.NewMemoryStore()
	storyStore := story.NewMemoryStore(ledStore)
	projStore := project.NewMemoryStore()
	contractStore := contract.NewMemoryStore(docStore, storyStore)
	taskStore := task.NewMemoryStore()

	ws, err := wsStore.Create(ctx, "user_a", "ws", now)
	require.NoError(t, err)
	proj, err := projStore.Create(ctx, "user_a", ws.ID, "p", now)
	require.NoError(t, err)

	developDoc, err := docStore.Create(ctx, document.Document{
		Type:       document.TypeContract,
		Scope:      document.ScopeSystem,
		Name:       "develop",
		Status:     document.StatusActive,
		Structured: []byte(`{"required_role":"developer","category":"build"}`),
	}, now)
	require.NoError(t, err)
	pushDoc, err := docStore.Create(ctx, document.Document{
		Type:       document.TypeContract,
		Scope:      document.ScopeSystem,
		Name:       "push",
		Status:     document.StatusActive,
		Structured: []byte(`{"required_role":"releaser","category":"release"}`),
	}, now)
	require.NoError(t, err)

	parent, err := storyStore.Create(ctx, story.Story{
		WorkspaceID: ws.ID,
		ProjectID:   proj.ID,
		Title:       "loop story",
		Status:      story.StatusInProgress,
	}, now)
	require.NoError(t, err)

	develop1, err := contractStore.Create(ctx, contract.ContractInstance{
		StoryID:      parent.ID,
		ContractID:   developDoc.ID,
		ContractName: "develop",
		Sequence:     1,
		Status:       contract.StatusReady,
	}, now)
	require.NoError(t, err)
	// Drive develop1 through its lifecycle: claimed → passed.
	_, err = contractStore.Claim(ctx, develop1.ID, "grant_dev1", now.Add(1*time.Minute), nil)
	require.NoError(t, err)
	_, err = contractStore.UpdateStatus(ctx, develop1.ID, contract.StatusFailed, "user_a", now.Add(2*time.Minute), nil)
	require.NoError(t, err)

	develop2, err := contractStore.Create(ctx, contract.ContractInstance{
		StoryID:      parent.ID,
		ContractID:   developDoc.ID,
		ContractName: "develop",
		Sequence:     1,
		Status:       contract.StatusReady,
		PriorCIID:    develop1.ID,
	}, now.Add(3*time.Minute))
	require.NoError(t, err)
	_, err = contractStore.Claim(ctx, develop2.ID, "grant_dev2", now.Add(4*time.Minute), nil)
	require.NoError(t, err)

	develop3, err := contractStore.Create(ctx, contract.ContractInstance{
		StoryID:      parent.ID,
		ContractID:   developDoc.ID,
		ContractName: "develop",
		Sequence:     1,
		Status:       contract.StatusReady,
		PriorCIID:    develop2.ID,
	}, now.Add(5*time.Minute))
	require.NoError(t, err)

	pushCI, err := contractStore.Create(ctx, contract.ContractInstance{
		StoryID:      parent.ID,
		ContractID:   pushDoc.ID,
		ContractName: "push",
		Sequence:     2,
		Status:       contract.StatusReady,
	}, now.Add(6*time.Minute))
	require.NoError(t, err)

	// Refresh CIs to capture latest state.
	develop1Refreshed, err := contractStore.GetByID(ctx, develop1.ID, nil)
	require.NoError(t, err)
	develop2Refreshed, err := contractStore.GetByID(ctx, develop2.ID, nil)
	require.NoError(t, err)
	develop3Refreshed, err := contractStore.GetByID(ctx, develop3.ID, nil)
	require.NoError(t, err)
	pushRefreshed, err := contractStore.GetByID(ctx, pushCI.ID, nil)
	require.NoError(t, err)

	// Two tasks bound to develop2 — one in-flight, one closed_success;
	// one task bound to develop3 — enqueued.
	_, err = taskStore.Enqueue(ctx, task.Task{
		WorkspaceID:        ws.ID,
		ProjectID:          proj.ID,
		ContractInstanceID: develop2.ID,
		Origin:             task.OriginStoryStage,
		Priority:           task.PriorityMedium,
	}, now)
	require.NoError(t, err)
	clm, err := taskStore.Claim(ctx, "worker_a", []string{ws.ID}, now.Add(time.Second))
	require.NoError(t, err)
	_, err = taskStore.Enqueue(ctx, task.Task{
		WorkspaceID:        ws.ID,
		ProjectID:          proj.ID,
		ContractInstanceID: develop2.ID,
		Origin:             task.OriginStoryStage,
		Priority:           task.PriorityMedium,
	}, now.Add(2*time.Second))
	require.NoError(t, err)
	clm2, err := taskStore.Claim(ctx, "worker_a", []string{ws.ID}, now.Add(3*time.Second))
	require.NoError(t, err)
	_, err = taskStore.Close(ctx, clm2.ID, task.OutcomeSuccess, now.Add(4*time.Second), nil)
	require.NoError(t, err)
	// avoid unused var warning
	_ = clm
	_, err = taskStore.Enqueue(ctx, task.Task{
		WorkspaceID:        ws.ID,
		ProjectID:          proj.ID,
		ContractInstanceID: develop3.ID,
		Origin:             task.OriginStoryStage,
		Priority:           task.PriorityMedium,
	}, now.Add(5*time.Second))
	require.NoError(t, err)

	// Append a couple of ledger rows scoped to develop2 so the row
	// count surfaces.
	for i := 0; i < 3; i++ {
		_, err = ledStore.Append(ctx, ledger.LedgerEntry{
			WorkspaceID: ws.ID,
			ProjectID:   proj.ID,
			StoryID:     ledger.StringPtr(parent.ID),
			ContractID:  ledger.StringPtr(develop2.ID),
			Type:        ledger.TypeDecision,
			Tags:        []string{"kind:test"},
			Content:     "row",
			CreatedBy:   "user_a",
		}, now.Add(time.Duration(i)*time.Second))
		require.NoError(t, err)
	}

	server := New(cfg, satarbor.New("info"), now, Deps{
		DocStore:       docStore,
		ProjectStore:   projStore,
		LedgerStore:    ledStore,
		StoryStore:     storyStore,
		WorkspaceStore: wsStore,
		ContractStore:  contractStore,
		TaskStore:      taskStore,
	})

	return &taskWalkFixture{
		t:         t,
		server:    server,
		caller:    CallerIdentity{UserID: "user_a", Source: "session"},
		wsID:      ws.ID,
		projectID: proj.ID,
		storyID:   parent.ID,
		developCIs: []contract.ContractInstance{
			develop1Refreshed,
			develop2Refreshed,
			develop3Refreshed,
		},
		pushCI: pushRefreshed,
		now:    now,
	}
}

// TestTaskWalk_HappyPath verifies the headline case: 3-lap develop loop
// + push CI returns ordered, iteration-grouped, current-pointer-aware
// payload.
func TestTaskWalk_HappyPath(t *testing.T) {
	t.Parallel()
	f := newTaskWalkFixture(t)
	ctx := context.WithValue(context.Background(), userKey, f.caller)

	req := mcpgo.CallToolRequest{}
	req.Params.Arguments = map[string]any{"story_id": f.storyID}
	res, err := f.server.handleTaskWalk(ctx, req)
	require.NoError(t, err)
	require.False(t, res.IsError, "task_walk: %+v", res)

	var resp taskWalkResponse
	require.NoError(t, json.Unmarshal([]byte(res.Content[0].(mcpgo.TextContent).Text), &resp))
	assert.Equal(t, f.storyID, resp.Story.ID)
	assert.Equal(t, "loop story", resp.Story.Title)
	require.Len(t, resp.ContractInstances, 4, "3 develop + 1 push")

	developIters := []int{}
	for _, ci := range resp.ContractInstances {
		if ci.ContractName == "develop" {
			developIters = append(developIters, ci.Iteration)
		}
	}
	assert.Equal(t, []int{1, 2, 3}, developIters, "develop laps numbered 1..3")
	for _, ci := range resp.ContractInstances {
		if ci.ContractName == "push" {
			assert.Equal(t, 1, ci.Iteration, "push is its first iteration")
			assert.Equal(t, "releaser", ci.RequiredRole)
			assert.Equal(t, "release", ci.ContractCategory)
		}
		if ci.ContractName == "develop" {
			assert.Equal(t, "developer", ci.RequiredRole)
		}
	}

	// current_ci_id points at develop2 — develop1 is failed (terminal),
	// so the first non-terminal CI walking sequence ASC is develop2.
	assert.Equal(t, f.developCIs[1].ID, resp.CurrentCIID)

	// Find develop2 in the response and check task summary + ledger
	// row count.
	var develop2 *taskWalkCI
	for i := range resp.ContractInstances {
		if resp.ContractInstances[i].ID == f.developCIs[1].ID {
			develop2 = &resp.ContractInstances[i]
			break
		}
	}
	require.NotNil(t, develop2)
	assert.Equal(t, 1, develop2.TaskSummary.InFlight)
	assert.Equal(t, 1, develop2.TaskSummary.ClosedSuccess)
	assert.Equal(t, 3, develop2.LedgerRowCount)
	assert.Equal(t, "claimed", develop2.Status)
	assert.NotNil(t, develop2.ClaimedAt)
	assert.Nil(t, develop2.ClosedAt, "claimed CI has no closed_at yet")

	// develop1 is terminal (failed) — outcome + closed_at populated.
	var develop1 *taskWalkCI
	for i := range resp.ContractInstances {
		if resp.ContractInstances[i].ID == f.developCIs[0].ID {
			develop1 = &resp.ContractInstances[i]
			break
		}
	}
	require.NotNil(t, develop1)
	assert.Equal(t, "failed", develop1.Status)
	assert.Equal(t, "failed", develop1.Outcome)
	require.NotNil(t, develop1.ClosedAt)
}

// TestTaskWalk_StoryNotFound returns a structured error.
func TestTaskWalk_StoryNotFound(t *testing.T) {
	t.Parallel()
	f := newTaskWalkFixture(t)
	ctx := context.WithValue(context.Background(), userKey, f.caller)
	req := mcpgo.CallToolRequest{}
	req.Params.Arguments = map[string]any{"story_id": "sty_doesnotexist"}
	res, err := f.server.handleTaskWalk(ctx, req)
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// TestTaskWalk_EmptyStory returns the story header + empty CI slice +
// null current_ci_id.
func TestTaskWalk_EmptyStory(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()
	wsStore := workspace.NewMemoryStore()
	docStore := document.NewMemoryStore()
	ledStore := ledger.NewMemoryStore()
	storyStore := story.NewMemoryStore(ledStore)
	projStore := project.NewMemoryStore()
	contractStore := contract.NewMemoryStore(docStore, storyStore)

	ws, err := wsStore.Create(ctx, "user_a", "ws", now)
	require.NoError(t, err)
	proj, err := projStore.Create(ctx, "user_a", ws.ID, "p", now)
	require.NoError(t, err)
	st, err := storyStore.Create(ctx, story.Story{
		WorkspaceID: ws.ID,
		ProjectID:   proj.ID,
		Title:       "fresh",
		Status:      story.StatusBacklog,
	}, now)
	require.NoError(t, err)

	server := New(&config.Config{}, satarbor.New("info"), now, Deps{
		DocStore:       docStore,
		ProjectStore:   projStore,
		LedgerStore:    ledStore,
		StoryStore:     storyStore,
		WorkspaceStore: wsStore,
		ContractStore:  contractStore,
		TaskStore:      task.NewMemoryStore(),
	})
	ctx = context.WithValue(ctx, userKey, CallerIdentity{UserID: "user_a", Source: "session"})
	req := mcpgo.CallToolRequest{}
	req.Params.Arguments = map[string]any{"story_id": st.ID}
	res, err := server.handleTaskWalk(ctx, req)
	require.NoError(t, err)
	require.False(t, res.IsError)
	var resp taskWalkResponse
	require.NoError(t, json.Unmarshal([]byte(res.Content[0].(mcpgo.TextContent).Text), &resp))
	assert.Empty(t, resp.ContractInstances)
	assert.Empty(t, resp.CurrentCIID)
	assert.Equal(t, st.ID, resp.Story.ID)
}
