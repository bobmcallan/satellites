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

//go:embed documents/*.md principles/*.md skills/*.md mcp/*.md workflows/*.md
var FS embed.FS

// BaselineWorkflowMarkdown returns the raw order-zero baseline workflow
// artifact bytes — the default story lifecycle init/rebase scaffold into a
// fresh repo's .satellites/workflows/. Homed in the client embed (not a Go
// const) so the default PROCESS is authored substrate, not baked-in code
// (epic:system-substrate).
func BaselineWorkflowMarkdown() []byte {
	b, err := fs.ReadFile(FS, "workflows/satellites-baseline-workflow.md")
	if err != nil {
		panic("substrate: workflows/satellites-baseline-workflow.md missing from embed.FS: " + err.Error())
	}
	return b
}

// ParentWorkflowMarkdown returns the raw default parent/epic workflow artifact
// bytes — the lifecycle an anchor (epic/parent) story follows, init/rebase
// scaffold into a fresh repo's .satellites/workflows/ alongside the baseline.
// Homed in the client embed (epic:system-substrate), not a Go const or a
// repo-only file.
func ParentWorkflowMarkdown() []byte {
	b, err := fs.ReadFile(FS, "workflows/satellites-parent-workflow.md")
	if err != nil {
		panic("substrate: workflows/satellites-parent-workflow.md missing from embed.FS: " + err.Error())
	}
	return b
}

// MCPLoadContextMarkdown returns the raw MCP load-context artifact
// bytes — the document the MCP server returns to every client on
// `initialize`. Held to the mcp_instructions_budget_bytes system
// variable; reference material lives in the separate
// satellites_mcp_reference_* artifacts which agents fetch on demand.
// Homed under mcp/ — the MCP-service substrate the server embeds and
// serves (epic:system-substrate); the authored-process substrate
// (skills/principles/documents) is the client binary's concern.
func MCPLoadContextMarkdown() []byte {
	b, err := fs.ReadFile(FS, "mcp/satellites_mcp_load_context.md")
	if err != nil {
		// The file is embedded at compile time — a missing read is a
		// build-time integrity break, not a runtime fallback.
		panic("substrate: mcp/satellites_mcp_load_context.md missing from embed.FS: " + err.Error())
	}
	return b
}

// ClientInstallMarkdown returns the raw install-schema artifact bytes.
// Homed under mcp/ as satellites_mcp_install — a CLI-less MCP client
// fetches it from the MCP service (epic:system-substrate).
func ClientInstallMarkdown() []byte {
	b, err := fs.ReadFile(FS, "mcp/satellites_mcp_install.md")
	if err != nil {
		panic("substrate: mcp/satellites_mcp_install.md missing from embed.FS: " + err.Error())
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
