// Package config embeds the canonical global-default substrate markdown
// artifacts into the satellites binaries. It is the SINGLE source of
// those bytes — no duplicate copy lives under internal/.
//
// Layout mirrors .satellites/: artifacts are filed by type into
// sibling subdirectories — documents/ (infra schemas, references,
// contracts), principles/ (the principles:* layer), and skills/
// (type:skill capability + reviewer bodies). Type is also declared in
// each artifact's YAML frontmatter (`scope`, `name`, `type`, optional
// `tags`); the reconciler reads identity from the frontmatter, NOT from
// the subdirectory — the subdir is for legibility. A new system-scope
// artifact is one new file in the matching subdir, with `scope: system`
// declared in its frontmatter.
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
package substrate

import (
	"embed"
	"io/fs"
)

//go:embed documents/*.md principles/*.md skills/*.md
var FS embed.FS

// MCPLoadContextMarkdown returns the raw MCP load-context artifact
// bytes — the document the MCP server returns to every client on
// `initialize`. Held to the mcp_instructions_budget_bytes system
// variable; reference material lives in the separate
// satellites_mcp_reference_* artifacts which agents fetch on demand.
func MCPLoadContextMarkdown() []byte {
	b, err := fs.ReadFile(FS, "documents/satellites_mcp_load_context.md")
	if err != nil {
		// The file is embedded at compile time — a missing read is a
		// build-time integrity break, not a runtime fallback.
		panic("substrate: documents/satellites_mcp_load_context.md missing from embed.FS: " + err.Error())
	}
	return b
}

// ClientInstallMarkdown returns the raw install-schema artifact bytes.
func ClientInstallMarkdown() []byte {
	b, err := fs.ReadFile(FS, "documents/satellites_client_install.md")
	if err != nil {
		panic("substrate: documents/satellites_client_install.md missing from embed.FS: " + err.Error())
	}
	return b
}

// SystemVariablesMarkdown returns the raw system-variables taxonomy
// artifact bytes — the operator-facing contract enumerating every
// computed system variable a document template can reference.
func SystemVariablesMarkdown() []byte {
	b, err := fs.ReadFile(FS, "documents/system_variables.md")
	if err != nil {
		panic("substrate: documents/system_variables.md missing from embed.FS: " + err.Error())
	}
	return b
}
