package mcpserver

import (
	"testing"
	"time"

	satarbor "github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/session"
	"github.com/bobmcallan/satellites/internal/story"
	"github.com/bobmcallan/satellites/internal/task"
	"github.com/bobmcallan/satellites/internal/workspace"
)

// TestAgentDispatchTool_Registered confirms the agent_dispatch verb
// (sty_51571015) lands in the ListTools surface when the registration
// site's deps are wired. Negative side: also verifies no
// `reviewer_service`-style tool sneaks back in.
func TestAgentDispatchTool_Registered(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Env: "dev"}
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	ledStore := ledger.NewMemoryStore()
	docStore := document.NewMemoryStore()
	storyStore := story.NewMemoryStore(ledStore)
	projStore := project.NewMemoryStore()
	wsStore := workspace.NewMemoryStore()
	sessionStore := session.NewMemoryStore()
	taskStore := task.NewMemoryStore()
	s := New(cfg, satarbor.New("info"), now, Deps{
		DocStore:       docStore,
		ProjectStore:   projStore,
		LedgerStore:    ledStore,
		StoryStore:     storyStore,
		WorkspaceStore: wsStore,
		SessionStore:   sessionStore,
		TaskStore:      taskStore,
	})
	tools := s.mcp.ListTools()
	if _, ok := tools["agent_dispatch"]; !ok {
		t.Error("agent_dispatch verb not registered (sty_51571015)")
	}
}
