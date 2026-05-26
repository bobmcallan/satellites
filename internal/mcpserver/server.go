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
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// inputSchemas maps each exposed verb to a typed JSON Schema option
// generated from its Go request struct. Without these, MCP clients
// (notably Claude web) have no field-type information and stringify
// array values — the dispatcher then rejects the call on unmarshal.
// Reflection-based generation keeps schema and struct in lock-step.
var inputSchemas = map[string]mcp.ToolOption{
	"document_get":    typedSchema[verb.DocumentGetRequest](),
	"document_list":   typedSchema[verb.DocumentListRequest](),
	"document_upsert": typedSchema[verb.DocumentUpsertRequest](),
	"document_delete": typedSchema[verb.DocumentDeleteRequest](),
	"project_match":   typedSchema[verb.ProjectMatchRequest](),
	"project_create":  typedSchema[verb.ProjectCreateRequest](),
	"project_list":    typedSchema[verb.ProjectListRequest](),
	"project_get":     typedSchema[verb.ProjectGetRequest](),
	"project_update":  typedSchema[verb.ProjectUpdateRequest](),
}

// typedSchema generates a JSON Schema from a Go request struct and
// strips "null" from union type arrays before handing it to mcp-go.
//
// The underlying jsonschema-go library encodes pointer-typed and
// slice-typed fields as ["null", T] unions — accurate JSON-Schema-wise,
// but enough to push some hosted clients (Claude web) into a fallback
// path that ships array values as JSON strings. Stripping "null"
// leaves a single, unambiguous type and the client emits the value
// as the declared array. Server-side decoding is unaffected: the
// verb dispatcher already treats JSON null and absent identically
// through omitempty + pointer semantics.
func typedSchema[T any]() mcp.ToolOption {
	schema, err := jsonschema.For[T](&jsonschema.ForOptions{IgnoreInvalidTypes: true})
	if err != nil {
		panic("mcpserver: typedSchema: " + err.Error())
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		panic("mcpserver: typedSchema marshal: " + err.Error())
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		panic("mcpserver: typedSchema unmarshal: " + err.Error())
	}
	stripNullable(doc)
	cleaned, err := json.Marshal(doc)
	if err != nil {
		panic("mcpserver: typedSchema remarshal: " + err.Error())
	}
	return mcp.WithRawInputSchema(cleaned)
}

// stripNullable walks a JSON-Schema document and rewrites every
// {"type": ["null", X]} union into {"type": X}.
func stripNullable(node any) {
	switch n := node.(type) {
	case map[string]any:
		if raw, ok := n["type"]; ok {
			if arr, ok := raw.([]any); ok {
				kept := arr[:0]
				for _, t := range arr {
					if s, _ := t.(string); s != "null" {
						kept = append(kept, t)
					}
				}
				switch len(kept) {
				case 0:
					delete(n, "type")
				case 1:
					n["type"] = kept[0]
				default:
					n["type"] = kept
				}
			}
		}
		for _, child := range n {
			stripNullable(child)
		}
	case []any:
		for _, child := range n {
			stripNullable(child)
		}
	}
}

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
//   - project_create / project_list / project_get / project_update:
//     project registration + maintenance for MCP-only clients (Claude
//     web, etc.) that cannot shell out to the CLI. project_create is
//     exposed deliberately — see layering_test.go for the denylist
//     entry that was lifted, and the operator memo behind it.
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
	"project_create",
	"project_list",
	"project_get",
	"project_update",
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
		schema, ok := inputSchemas[name]
		if !ok {
			panic("mcpserver: exposed verb " + name + " missing input schema")
		}
		dispatched := name
		s.AddTool(
			mcp.NewTool(name, mcp.WithDescription(v.Description), schema),
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
