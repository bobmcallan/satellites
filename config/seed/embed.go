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

//go:embed system/artifacts/system_variables.md
var systemVariablesMD []byte

// ClientInstallMarkdown returns the raw install-schema artifact bytes.
func ClientInstallMarkdown() []byte { return clientInstallMD }

// MCPLoadContextMarkdown returns the raw MCP load-context artifact
// bytes — the document the MCP server returns to every client on
// `initialize`.
func MCPLoadContextMarkdown() []byte { return mcpLoadContextMD }

// SystemVariablesMarkdown returns the raw system-variables taxonomy
// artifact bytes — the operator-facing contract enumerating every
// computed system variable a document template can reference.
func SystemVariablesMarkdown() []byte { return systemVariablesMD }
