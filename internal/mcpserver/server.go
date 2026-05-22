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

	"github.com/bobmcallan/satellites/config/seed"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// orientationInstructions is returned in the MCP `initialize` response
// so clients (Claude, Warp, Codex, …) read it as session-bootstrap
// context. Sourced from the embedded markdown artifact
// `config/seed/system/artifacts/satellites_mcp_load_context.md` —
// configuration-over-code: agent-facing prose lives in markdown, this
// file just loads it.
var orientationInstructions = string(seed.MCPLoadContextMarkdown())

// bootstrapVerb is the single verb exposed over MCP. Every other
// substrate operation is reachable via the satellites CLI (`satellites
// exec <verb>`) once the agent has bootstrapped the binary using the
// orientation instructions. Keeping the MCP surface minimal avoids
// duplicating the verb catalog into the agent's tool list — the CLI is
// the primary client.
//
// document_get is the single MCP tool. The load context instructs the
// agent to call it with name=satellites_client_install, scope=system
// to obtain the install schema (templated install URLs, target paths,
// TOML defaults, bootstrap-auth flow).
const bootstrapVerb = "document_get"

// New returns a configured *mcpserver.MCPServer exposing only the
// bootstrap verb. The orientation instructions tell the agent to
// install/refresh the satellites CLI and dispatch all other verbs
// through it.
func New() *mcpserver.MCPServer {
	s := mcpserver.NewMCPServer("satellites", verb.Version,
		mcpserver.WithInstructions(orientationInstructions),
	)

	v := verb.Get(bootstrapVerb)
	if v == nil {
		panic("mcpserver: bootstrap verb " + bootstrapVerb + " not registered")
	}
	s.AddTool(
		mcp.NewTool(bootstrapVerb, mcp.WithDescription(v.Description)),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			argsJSON, err := json.Marshal(req.GetArguments())
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			resp, err := verb.Dispatch(ctx, bootstrapVerb, argsJSON)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(string(resp)), nil
		},
	)
	return s
}

// HTTPHandler returns an http.Handler serving the MCP server over
// streamable HTTP (MCP spec's recommended HTTP transport).
func HTTPHandler(s *mcpserver.MCPServer) http.Handler {
	return mcpserver.NewStreamableHTTPServer(s)
}
