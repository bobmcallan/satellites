package mcpserver

import (
	"testing"

	"github.com/bobmcallan/satellites/internal/layeringtest"
)

// TestNoSubstrateDomainImports enforces pr_mcp_cli_shared_path: the
// transport layer (this package) must NOT import substrate-domain
// packages directly. Every business-logic touch flows through
// internal/verb's Dispatch. Peer transports (internal/cli,
// internal/server) are also forbidden — transports must not import
// each other.
func TestNoSubstrateDomainImports(t *testing.T) {
	layeringtest.RunGuard(t,
		"github.com/bobmcallan/satellites/internal/cli",
		"github.com/bobmcallan/satellites/internal/server",
	)
}

// TestExposedVerbsDoNotIncludeCLIOnlyVerbs guards against accidental
// growth of the MCP surface. Some verbs are intentionally CLI-only:
//
//   - variable_*       — secret/config management; CLI auth flow only.
//   - workspace_*      — admin operations; not in scope for MCP clients.
//   - ledger_*         — append-only audit, not authoring.
//   - system_seed_*    — system-scope mutation, operator-only.
//   - project_seed_*   — same.
//   - story_*          — stories are documents post-unification; the
//     document_* surface is canonical. Re-introducing story_* verbs
//     would split the surface.
//
// project_create was previously on this list as "administrative
// provisioning, not authoring", but MCP-only clients (Claude web) need
// to register a missing project without shelling out to the CLI. The
// entry was lifted deliberately; see exposedVerbs in server.go.
//
// If one of these names appears in exposedVerbs, the change is almost
// certainly a mistake. Lift the denylist explicitly with a follow-up
// story rather than relaxing this test.
func TestExposedVerbsDoNotIncludeCLIOnlyVerbs(t *testing.T) {
	deny := []string{
		"variable_get", "variable_set", "variable_list", "variable_delete",
		"workspace_create", "workspace_list", "workspace_get", "workspace_delete", "workspace_personal",
		"workspace_member_add", "workspace_member_remove", "workspace_member_list", "workspace_member_update",
		"project_member_add", "project_member_remove", "project_member_list", "project_member_update_role", "project_access",
		"invitation_create", "invitation_list", "invitation_revoke",
		"ledger_append", "ledger_list",
		"system_seed_set", "system_seed_list", "system_seed_delete",
		"project_seed_set", "project_seed_list", "project_seed_delete",
		"story_create", "story_update", "story_get", "story_delete", "story_list",
	}
	exposed := map[string]bool{}
	for _, n := range exposedVerbs {
		exposed[n] = true
	}
	for _, n := range deny {
		if exposed[n] {
			t.Errorf("MCP surface includes CLI-only verb %q — confirm intent before relaxing", n)
		}
	}
}
