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
type Frontmatter struct {
	Tags []string `yaml:"tags"`
	Name string   `yaml:"name"`
}

// frontmatterDelim is the literal `---` line that opens and closes a
// YAML frontmatter block in markdown files.
var frontmatterDelim = []byte("---")

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
