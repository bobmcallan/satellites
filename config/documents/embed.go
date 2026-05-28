// Package documents embeds the canonical substrate markdown artifacts
// in this directory into the satellites-server binary. It is the
// SINGLE source of those bytes — no duplicate copy lives under
// internal/.
//
// Layout is flat: every artifact is a sibling .md in this directory
// and declares its identity in YAML frontmatter (`scope`, `name`,
// optional `workspace_id` / `project_id`, optional `tags`). The
// reconciler reads scope from frontmatter rather than from the
// directory layout — a new system-scope artifact is one new file in
// this directory, with `scope: system` declared in its frontmatter.
//
// Configuration over code: agent-facing prose, install schemas,
// reviewer rubrics, and skill bodies all live as markdown under this
// directory. Go is the load layer; the substance is the .md.
//
// Consumers:
//   - cmd/satellites-server — walks FS at boot and reconciles each
//     row into the documents table according to its frontmatter
//     scope.
//   - internal/mcpserver — returns the MCP load-context artifact as
//     the `initialize` instructions for every connecting client.
package documents

import (
	"embed"
	"io/fs"
)

//go:embed *.md
var FS embed.FS

// MCPLoadContextMarkdown returns the raw MCP load-context artifact
// bytes — the document the MCP server returns to every client on
// `initialize`. Held to the mcp_instructions_budget_bytes system
// variable; reference material lives in the separate
// satellites_mcp_reference_* artifacts which agents fetch on demand.
func MCPLoadContextMarkdown() []byte {
	b, err := fs.ReadFile(FS, "satellites_mcp_load_context.md")
	if err != nil {
		// The file is embedded at compile time — a missing read is a
		// build-time integrity break, not a runtime fallback.
		panic("documents: satellites_mcp_load_context.md missing from embed.FS: " + err.Error())
	}
	return b
}

// ClientInstallMarkdown returns the raw install-schema artifact bytes.
func ClientInstallMarkdown() []byte {
	b, err := fs.ReadFile(FS, "satellites_client_install.md")
	if err != nil {
		panic("documents: satellites_client_install.md missing from embed.FS: " + err.Error())
	}
	return b
}

// SystemVariablesMarkdown returns the raw system-variables taxonomy
// artifact bytes — the operator-facing contract enumerating every
// computed system variable a document template can reference.
func SystemVariablesMarkdown() []byte {
	b, err := fs.ReadFile(FS, "system_variables.md")
	if err != nil {
		panic("documents: system_variables.md missing from embed.FS: " + err.Error())
	}
	return b
}
