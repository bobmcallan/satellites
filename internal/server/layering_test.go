package server

import (
	"testing"

	"github.com/bobmcallan/satellites/internal/layeringtest"
)

// TestNoSubstrateDomainImports enforces pr_mcp_cli_shared_path on the
// HTTP/portal transport: every business-logic touch flows through
// internal/verb's Dispatch. Peer transports (internal/cli,
// internal/mcpserver) are also forbidden — note this package may
// import internal/mcpserver to mount the MCP handler, which would be
// a transport composition, not a transport-to-transport call; if that
// composition is ever needed, surface it through a separate seam.
func TestNoSubstrateDomainImports(t *testing.T) {
	layeringtest.RunGuard(t,
		"github.com/bobmcallan/satellites/internal/cli",
	)
}
