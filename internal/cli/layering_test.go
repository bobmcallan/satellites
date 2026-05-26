package cli

import (
	"testing"

	"github.com/bobmcallan/satellites/internal/layeringtest"
)

// TestNoSubstrateDomainImports enforces pr_mcp_cli_shared_path on the
// CLI transport: every business-logic touch flows through
// internal/verb's Dispatch. Peer transports (internal/mcpserver,
// internal/server) are also forbidden.
func TestNoSubstrateDomainImports(t *testing.T) {
	layeringtest.RunGuard(t,
		"github.com/bobmcallan/satellites/internal/mcpserver",
		"github.com/bobmcallan/satellites/internal/server",
	)
}
