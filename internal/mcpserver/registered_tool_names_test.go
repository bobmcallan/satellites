package mcpserver

import (
	"testing"
	"time"

	satarbor "github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/session"
	"github.com/bobmcallan/satellites/internal/story"
	"github.com/bobmcallan/satellites/internal/workspace"
)

// TestRegisteredToolNames_RenameFlatten asserts the v4 verb-namespace
// flatten (story_775a7b49): the contract / workflow MCP tools register
// under their flat names; the prior story_*-prefixed names are absent.
//
// Wires every store the registration site gates on so all six renamed
// verbs are advertised. The integration counterpart in
// tests/integration/mcp_test.go boots a bare DEV container without a
// DB and therefore only checks the old-names-absent half.
func TestRegisteredToolNames_RenameFlatten(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Env: "dev"}
	now := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)
	docStore := document.NewMemoryStore()
	ledStore := ledger.NewMemoryStore()
	storyStore := story.NewMemoryStore(ledStore)
	projStore := project.NewMemoryStore()
	contractStore := contract.NewMemoryStore(docStore, storyStore)
	wsStore := workspace.NewMemoryStore()
	sessionStore := session.NewMemoryStore()

	server := New(cfg, satarbor.New("info"), now, Deps{
		DocStore:       docStore,
		ProjectStore:   projStore,
		LedgerStore:    ledStore,
		StoryStore:     storyStore,
		ContractStore:  contractStore,
		WorkspaceStore: wsStore,
		SessionStore:   sessionStore,
	})

	tools := server.mcp.ListTools()

	for _, want := range []string{
		"workflow_claim",
		"contract_next",
		"contract_claim",
		"contract_close",
		"contract_resume",
		"contract_respond",
	} {
		if _, ok := tools[want]; !ok {
			t.Errorf("registered tools missing %q (story_775a7b49 rename)", want)
		}
	}
	for _, gone := range []string{
		"story_workflow_claim",
		"story_contract_next",
		"story_contract_claim",
		"story_contract_close",
		"story_contract_resume",
		"story_contract_respond",
	} {
		if _, ok := tools[gone]; ok {
			t.Errorf("registered tools still expose retired name %q (story_775a7b49 rename)", gone)
		}
	}
}
