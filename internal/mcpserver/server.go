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

// exposedVerbs are the verbs the MCP HTTP transport advertises and
// dispatches. The surface is intentionally narrow:
//
//   - project_match (bootstrap): resolves the consumer repo's git
//     remote to a project_id. Native CLI-driven agents use this once
//     during install; the document_* verbs cover everything after.
//   - document_get / document_list / document_upsert / document_delete:
//     the unified CRUD surface across both substrate kinds — pass
//     type:"story" on writes/queries to operate on stories,
//     type:"document" (default) for free-form documents.
//
// Stories are documents with type='story' post-unification (sty_0dd71f79);
// there are no story_* verbs on the surface. The CLI offers no typed
// wrappers either — operators dispatch document_* directly.
//
// Layering is preserved: each dispatch call still goes through
// verb.Dispatch, so all transports share one execution path.
var exposedVerbs = []string{
	"document_get",
	"document_list",
	"document_upsert",
	"document_delete",
	"project_match",
}

// New returns a configured *mcpserver.MCPServer exposing the verbs in
// exposedVerbs. The orientation instructions tell native CLI agents to
// install the binary and run every other verb through it; MCP-only
// agents read the same orientation and use the directly-exposed write
// verbs documented there.
func New() *mcpserver.MCPServer {
	s := mcpserver.NewMCPServer("satellites", verb.Version,
		mcpserver.WithInstructions(orientationInstructions),
	)

	for _, name := range exposedVerbs {
		v := verb.Get(name)
		if v == nil {
			panic("mcpserver: exposed verb " + name + " not registered")
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
