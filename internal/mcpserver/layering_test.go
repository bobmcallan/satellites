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
