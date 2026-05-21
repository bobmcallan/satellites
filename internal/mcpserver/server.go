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
//
// Two distinct auth surfaces, intentionally:
//   - MCP session (this connection): authenticated via OAuth. The
//     .mcp.json on the consumer side carries no bearer; the MCP SDK
//     drives the AS discovery + DCR + authorize + token flow.
//   - CLI ↔ server (satellites exec): authenticated via an api-key
//     persisted at target_config_path. The api-key satellites_init
//     mints below is FOR THE CLI to embed there — not for this MCP
//     session.
const orientationInstructions = `satellites MCP server.

This server exposes a verb registry. Both the satellites CLI and MCP
clients dispatch through the same verbs — there is exactly one
implementation per verb.

BEFORE doing any other work, call the satellites_init tool, then
perform the install sequence below in order:

  1. Read state. install_required | update_available | up_to_date.
     Skip steps 2-4 when up_to_date.

  2. Download the CLI binary. Fetch install.download_url, verify
     against install.sha256_url, mkdir -p the parent of
     target_install_path, write the binary there, chmod +x.

  3. Materialise the CLI config at target_config_path
     (typically ./.satellites/satellites.toml). Use default_config
     as the TOML body. Substitute auth_bootstrap.api_key into the
     [auth].token field when auth_bootstrap.kind=ready. When
     kind=auth_login the [auth].token stays empty and the operator
     mints a key out of band via auth_bootstrap.command.

  4. Verify by running target_install_path with 'version'. The CLI
     reads its TOML at boot and prints the server it's bound to.

Two auth surfaces, intentionally distinct:
  - This MCP session authenticates via OAuth. The consumer-side
    .mcp.json carries no bearer; the MCP SDK drives the AS discovery
    + DCR + authorize + token flow.
  - The CLI authenticates with the api-key persisted in
    .satellites/satellites.toml's [auth].token. The key
    satellites_init returns is FOR THAT TOML — never reuse it here.

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
