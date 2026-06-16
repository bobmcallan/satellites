// Package frontmatter parses the YAML frontmatter block that prefixes
// substrate markdown artifacts. Shared by the boot system-seed
// reconciler (cmd/satellites-server) and the file-based document
// uploader (internal/cli) — placed outside internal/document so the
// CLI layering guard (pr_mcp_cli_shared_path) is not crossed.
package frontmatter

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// Frontmatter is the subset of YAML frontmatter fields the substrate
// reads from .md artifacts. Fields not declared here are silently
// ignored on parse — the loader stays forward-compatible with files
// that grow new keys for downstream tools.
//
// Scope / WorkspaceID / ProjectID carry the identity that was
// previously inferred from directory path segments. Files in
// config/documents/ and .satellites/documents/ declare them
// explicitly; the classifier and reconciler read them from this
// struct, not from the filesystem layout.
type Frontmatter struct {
	Tags        []string `yaml:"tags"`
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Type        string   `yaml:"type"`

	// Headline is a terse, drift-free one-liner (caveman form) describing a
	// document or principle. Authored here and stored on the row so the
	// `satellites document index` listing stays context-cheap without loading
	// bodies. Empty when unset (epic:always-context).
	Headline    string `yaml:"headline"`
	Scope       string `yaml:"scope"`
	WorkspaceID string `yaml:"workspace_id"`
	ProjectID   string `yaml:"project_id"`

	// Skill dispatch contract (sty_3359cb48). Carried by type:skill sources
	// so the dynamic index can dispatch off frontmatter without loading
	// bodies: Kind classifies the skill, AppliesTo binds it to story types
	// (the single source, replacing project-config story_types), When is the
	// status/guard at which it applies.
	Kind      string   `yaml:"kind"`
	AppliesTo []string `yaml:"applies_to"`
	When      string   `yaml:"when"`

	// Level is the visibility an artifact DECLARES (epic:client-dir-separation
	// order-4): system / project / global — one vocabulary across all kinds.
	// It RESOLVES to a storage scope (global→library, project→project,
	// system→system); publish routes by it. Empty falls back to Scope.
	Level string `yaml:"level"`

	// Tools is the agent capability allowlist a kind:task skill declares
	// (epic:workspace-agents): the policy of which tools the server agent may
	// call lives in configuration here, not in the binary. Names are resolved
	// against the harness's tool catalogue at run time.
	Tools []string `yaml:"tools"`
}

// frontmatterDelim is the literal `---` line that opens and closes a
// YAML frontmatter block in markdown files.
var frontmatterDelim = []byte("---")

// syncStampBegin / syncStampEnd bound the client-injected sync-identity
// comment that a materialised .claude/skills/<name>/SKILL.md carries —
// on the line after its frontmatter close (current layout, so Claude
// Code reads the frontmatter first) or, in legacy files, ahead of the
// frontmatter (document_id/version/hash; see
// internal/cli/cmd_skill_sync.go, which writes it). The block is local
// bookkeeping, never part of the authored content — so any reader that
// parses the authored frontmatter or body must see past both positions.
var (
	syncStampBegin = []byte("<!-- satellites-sync:begin")
	syncStampEnd   = []byte("satellites-sync:end -->")
)

// StripSyncStamp removes a leading sync-identity comment block if the
// bytes begin with one, returning the remainder with one following
// newline consumed. No stamp (or a malformed one with no closing
// marker) → input returned unchanged. Idempotent: stripping
// already-clean bytes is a no-op.
func StripSyncStamp(raw []byte) []byte {
	trimmed := bytes.TrimLeft(raw, " \t\r\n")
	if !bytes.HasPrefix(trimmed, syncStampBegin) {
		return raw
	}
	endIdx := bytes.Index(trimmed, syncStampEnd)
	if endIdx < 0 {
		return raw
	}
	return trimLeadingNewline(trimmed[endIdx+len(syncStampEnd):])
}

// Parse splits a file's bytes into a typed frontmatter
// struct and the trailing body. Behaviour:
//
//   - File without an opening `---\n` on the first line → returns zero
//     frontmatter and the whole input as body. Mirrors how the existing
//     embedded artifacts (no frontmatter today) keep working unchanged.
//   - File with `---\n…---\n` on lines 1+ → unmarshals the inner block
//     into Frontmatter; body is everything after the closing delimiter.
//   - Malformed (opening delimiter but no closing one, or YAML that
//     can't decode) → returns an error so callers can fail loudly
//     instead of silently shipping the wrong tags.
//
// Line endings are tolerant of CRLF — the parser splits on '\n' and
// strips trailing '\r' from each line.
func Parse(raw []byte) (Frontmatter, []byte, error) {
	raw = StripSyncStamp(raw)
	if !bytes.HasPrefix(raw, append(frontmatterDelim, '\n')) && !bytes.HasPrefix(raw, append(frontmatterDelim, '\r', '\n')) {
		return Frontmatter{}, raw, nil
	}
	// Skip the opening delimiter line.
	rest := raw[len(frontmatterDelim):]
	rest = trimLeadingNewline(rest)

	closeIdx := indexClosingDelimiter(rest)
	if closeIdx < 0 {
		return Frontmatter{}, nil, fmt.Errorf("frontmatter: opening --- without matching closing ---")
	}
	yamlBlock := rest[:closeIdx]
	body := rest[closeIdx+len(frontmatterDelim):]
	body = trimLeadingNewline(body)
	// A sync stamp on the line after the frontmatter close (the current
	// materialised layout) is bookkeeping, not authored body.
	body = StripSyncStamp(body)

	var fm Frontmatter
	if len(bytes.TrimSpace(yamlBlock)) > 0 {
		if err := yaml.Unmarshal(yamlBlock, &fm); err != nil {
			return Frontmatter{}, nil, fmt.Errorf("frontmatter: yaml: %w", err)
		}
	}
	return fm, body, nil
}

// indexClosingDelimiter finds the byte offset of a `---` line within
// the supplied slice. Returns -1 when no such line exists. Looks only
// for a delimiter at the start of a line — `---` inside the YAML body
// would be invalid YAML anyway, but the line-start check stops a
// matching substring inside a value from being misread as a close.
func indexClosingDelimiter(s []byte) int {
	offset := 0
	for len(s) > 0 {
		nl := bytes.IndexByte(s, '\n')
		var line []byte
		if nl < 0 {
			line = s
		} else {
			line = s[:nl]
		}
		trimmed := bytes.TrimRight(line, "\r")
		if bytes.Equal(trimmed, frontmatterDelim) {
			return offset
		}
		if nl < 0 {
			return -1
		}
		offset += nl + 1
		s = s[nl+1:]
	}
	return -1
}

// trimLeadingNewline removes one leading "\n" or "\r\n" if present.
// Used between the frontmatter delimiters and the body to keep the
// body's first line aligned with what the operator authored.
func trimLeadingNewline(s []byte) []byte {
	switch {
	case bytes.HasPrefix(s, []byte("\r\n")):
		return s[2:]
	case bytes.HasPrefix(s, []byte("\n")):
		return s[1:]
	}
	return s
}
