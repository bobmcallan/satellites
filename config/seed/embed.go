// Package seed embeds the canonical substrate-seed markdown artifacts
// under config/seed/system/artifacts/ into the satellites-server
// binary. It is the SINGLE source of those bytes — no duplicate copy
// lives under internal/.
//
// Configuration over code: agent-facing prose, install schemas, and
// reviewer rubrics all live as markdown under this directory. Go is
// the load layer; the substance is the .md. Editing a .md in this
// tree IS the way to change substrate behaviour — no separate Go
// const, no embedded copy under internal/ to sync.
//
// Consumers:
//   - cmd/satellites-server — seeds each artifact as a scope=system
//     document at boot so document_get retrieves it from the store.
//   - internal/mcpserver   — returns the MCP load-context artifact as
//     the `initialize` instructions for every connecting client.
package seed

import _ "embed"

//go:embed system/artifacts/satellites_client_install.md
var clientInstallMD []byte

//go:embed system/artifacts/satellites_mcp_load_context.md
var mcpLoadContextMD []byte

//go:embed system/artifacts/satellites_mcp_reference_dispatch.md
var mcpReferenceDispatchMD []byte

//go:embed system/artifacts/satellites_mcp_reference_documents.md
var mcpReferenceDocumentsMD []byte

//go:embed system/artifacts/system_variables.md
var systemVariablesMD []byte

//go:embed system/artifacts/principle-configuration-over-code.md
var principleConfigurationOverCodeMD []byte

// ClientInstallMarkdown returns the raw install-schema artifact bytes.
func ClientInstallMarkdown() []byte { return clientInstallMD }

// MCPLoadContextMarkdown returns the raw MCP load-context artifact
// bytes — the document the MCP server returns to every client on
// `initialize`. Held to the mcp_instructions_budget_bytes system
// variable; reference material lives in the separate
// satellites_mcp_reference_* artifacts which agents fetch on demand.
func MCPLoadContextMarkdown() []byte { return mcpLoadContextMD }

// MCPReferenceDispatchMarkdown returns the raw bytes of the CLI
// dispatch reference — Step-5 content fetched on demand via
// document_get rather than shipped in the initialize blob.
func MCPReferenceDispatchMarkdown() []byte { return mcpReferenceDispatchMD }

// MCPReferenceDocumentsMarkdown returns the raw bytes of the document
// and story reference — upsert modes, list filter shape, MCP-only
// client surface. Fetched on demand via document_get.
func MCPReferenceDocumentsMarkdown() []byte { return mcpReferenceDocumentsMD }

// SystemVariablesMarkdown returns the raw system-variables taxonomy
// artifact bytes — the operator-facing contract enumerating every
// computed system variable a document template can reference.
func SystemVariablesMarkdown() []byte { return systemVariablesMD }

// PrincipleConfigurationOverCodeMarkdown returns the raw bytes of the
// global "configuration over code" principle. The file carries
// frontmatter tagging it `principles:global`, so the system-seed
// reconciler delivers it via the principles sidecar on MCP initialize.
func PrincipleConfigurationOverCodeMarkdown() []byte { return principleConfigurationOverCodeMD }
