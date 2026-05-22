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

// bootstrapVerbs are the verbs exposed over MCP. Every operational
// substrate verb is reachable via the satellites CLI once the agent has
// bootstrapped the binary using the orientation instructions. Keeping
// the MCP surface minimal avoids duplicating the verb catalog into the
// agent's tool list — the CLI is the primary client.
//
// The bootstrap surface is intentionally two-step:
//   - document_get returns the install schema (templated install URLs,
//     target paths, TOML defaults, bootstrap-auth flow).
//   - project_match resolves the consumer repo's git remote to a
//     project_id so the agent can persist it into the TOML.
//
// Together they are everything a fresh agent needs to install the CLI
// and configure it against the right project.
var bootstrapVerbs = []string{"document_get", "project_match"}

// New returns a configured *mcpserver.MCPServer exposing the bootstrap
// verbs. The orientation instructions tell the agent to install/refresh
// the satellites CLI and dispatch every other verb through it.
func New() *mcpserver.MCPServer {
	s := mcpserver.NewMCPServer("satellites", verb.Version,
		mcpserver.WithInstructions(orientationInstructions),
	)

	for _, name := range bootstrapVerbs {
		v := verb.Get(name)
		if v == nil {
			panic("mcpserver: bootstrap verb " + name + " not registered")
		}
		dispatched := name
		s.AddTool(
			mcp.NewTool(name, mcp.WithDescription(v.Description)),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				argsJSON, err := json.Marshal(req.GetArguments())
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				resp, err := verb.Dispatch(ctx, dispatched, argsJSON)
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
