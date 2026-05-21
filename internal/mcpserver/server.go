// Package mcpserver wraps the verb registry behind MCP-HTTP transport.
//
// This package holds no substrate business logic — every MCP tool call
// dispatches to verb.Dispatch. The pr_mcp_cli_shared_path discipline
// (carried forward from V4) is enforced by the AST-based layering test
// in this package.
package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// orientationInstructions is returned in the MCP `initialize` response
// so clients (Claude, etc.) read it as session-bootstrap context. The
// prose nudges the agent to call satellites_init first; that verb then
// returns the install/update + auth bootstrap shape sourced from the
// embedded install-schema markdown.
const orientationInstructions = `satellites MCP server.

This server exposes a verb registry. Both the satellites CLI and MCP
clients dispatch through the same verbs — there is exactly one
implementation per verb.

BEFORE doing any other work, call the satellites_init tool. The
response includes:

  - Whether the local satellites CLI is missing, out-of-date, or
    current (state = install_required | update_available | up_to_date).
  - The download URL + sha256 URL for the current CLI release matching
    your OS/arch. Fetch + verify + install at target_install_path.
  - The canonical TOML config shape (default_config) the CLI expects
    at target_config_path.
  - An auth_bootstrap block. When you're authenticated (Bearer api-key
    on this MCP session), kind=ready and a fresh api-key is minted
    inline. Otherwise kind=auth_login with the command to run.

After install/update, prefer 'satellites exec <verb>' for heavy work —
the MCP transport is intentionally light. Use tools/list to see every
verb available.`

// New returns a configured *mcpserver.MCPServer with every registered
// verb attached as an MCP tool.
func New() *mcpserver.MCPServer {
	s := mcpserver.NewMCPServer("satellites", verb.Version,
		mcpserver.WithInstructions(orientationInstructions),
	)

	for _, name := range verb.Catalog() {
		v := verb.Get(name)
		name, v := name, v // capture per-iteration

		s.AddTool(
			mcp.NewTool(name, mcp.WithDescription(v.Description)),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				argsJSON, err := json.Marshal(req.GetArguments())
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				resp, err := verb.Dispatch(ctx, name, argsJSON)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				return mcp.NewToolResultText(string(resp)), nil
			},
		)
	}
	return s
}

// HTTPHandler returns an http.Handler serving the MCP server over
// streamable HTTP (MCP spec's recommended HTTP transport).
func HTTPHandler(s *mcpserver.MCPServer) http.Handler {
	return mcpserver.NewStreamableHTTPServer(s)
}
