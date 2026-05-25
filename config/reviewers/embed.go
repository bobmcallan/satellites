// Package reviewers embeds the substrate's built-in reviewer agent
// definitions (markdown files in this directory) into the
// satellites-server binary.
//
// Each *.md file is a complete reviewer: YAML frontmatter for the
// (model, max_tokens, enabled) tuning knobs, body for the system
// prompt + rubric + output schema. internal/reviewer is the loader
// + orchestrator; it consumes this embedded fs.FS at boot and
// dispatches against the configured LLM client.
//
// Adding a new reviewer is one new file in this directory — no Go
// source change is required.
package reviewers

import "embed"

//go:embed *.md
var FS embed.FS
