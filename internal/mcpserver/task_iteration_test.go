package mcpserver

import (
	"context"
	"testing"
	"time"

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

// TestStampTaskIteration_CountsLapsAcrossSameContractName verifies
// sty_78ddc67b: when multiple CIs of the same contract_name live on the
// same story (rejection-append loop), tasks bound to each CI are stamped
// with their lap number.
func TestStampTaskIteration_CountsLapsAcrossSameContractName(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)

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
		Type:   document.TypeContract,
		Scope:  document.ScopeSystem,
		Name:   "develop",
		Status: document.StatusActive,
	}, now)
	require.NoError(t, err)
	pushDoc, err := docStore.Create(ctx, document.Document{
		Type:   document.TypeContract,
		Scope:  document.ScopeSystem,
		Name:   "push",
		Status: document.StatusActive,
	}, now)
	require.NoError(t, err)
	parent, err := storyStore.Create(ctx, story.Story{
		WorkspaceID: ws.ID,
		ProjectID:   proj.ID,
		Title:       "loop story",
	}, now)
	require.NoError(t, err)

	// Three develop CIs (rejection-append loop) plus one push CI.
	develop1, err := contractStore.Create(ctx, contract.ContractInstance{
		StoryID:      parent.ID,
		ContractID:   developDoc.ID,
		ContractName: "develop",
		Sequence:     1,
		Status:       contract.StatusReady,
	}, now)
	require.NoError(t, err)
	develop2, err := contractStore.Create(ctx, contract.ContractInstance{
		StoryID:      parent.ID,
		ContractID:   developDoc.ID,
		ContractName: "develop",
		Sequence:     1,
		Status:       contract.StatusReady,
		PriorCIID:    develop1.ID,
	}, now.Add(time.Minute))
	require.NoError(t, err)
	develop3, err := contractStore.Create(ctx, contract.ContractInstance{
		StoryID:      parent.ID,
		ContractID:   developDoc.ID,
		ContractName: "develop",
		Sequence:     1,
		Status:       contract.StatusReady,
		PriorCIID:    develop2.ID,
	}, now.Add(2*time.Minute))
	require.NoError(t, err)
	pushCI, err := contractStore.Create(ctx, contract.ContractInstance{
		StoryID:      parent.ID,
		ContractID:   pushDoc.ID,
		ContractName: "push",
		Sequence:     2,
		Status:       contract.StatusReady,
	}, now.Add(3*time.Minute))
	require.NoError(t, err)

	server := New(&config.Config{}, satarbor.New("info"), now, Deps{
		DocStore:       docStore,
		ProjectStore:   projStore,
		LedgerStore:    ledStore,
		StoryStore:     storyStore,
		WorkspaceStore: wsStore,
		ContractStore:  contractStore,
		TaskStore:      taskStore,
	})

	cases := []struct {
		ciID        string
		wantIter    int
		description string
	}{
		{develop1.ID, 1, "first develop lap"},
		{develop2.ID, 2, "second develop lap"},
		{develop3.ID, 3, "third develop lap"},
		{pushCI.ID, 1, "push contract — fresh name"},
	}
	for _, tc := range cases {
		seed := server.stampTaskIteration(ctx, task.Task{
			WorkspaceID:        ws.ID,
			ProjectID:          proj.ID,
			ContractInstanceID: tc.ciID,
			Origin:             task.OriginStoryStage,
			Priority:           task.PriorityMedium,
		}, nil)
		assert.Equal(t, tc.wantIter, seed.Iteration, tc.description)
	}

	// CI-less task: helper passes through untouched, store-layer default
	// of 1 applies on Enqueue.
	bare := server.stampTaskIteration(ctx, task.Task{
		WorkspaceID: ws.ID,
		ProjectID:   proj.ID,
		Origin:      task.OriginStoryStage,
		Priority:    task.PriorityMedium,
	}, nil)
	assert.Equal(t, 0, bare.Iteration, "no CI → helper leaves zero, store stamps 1 on Enqueue")
}
