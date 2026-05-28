// Package reviewer is the substrate's markdown-defined story
// reviewer framework. Every reviewer (story_reviewer, story_summary,
// future critics) lives as a single markdown file with YAML
// frontmatter for the tuning knobs (model, max_tokens, enabled)
// and a body that is the literal system prompt + rubric + output
// schema. NO reviewer prose lives in Go source — this package is
// load-render-call-parse orchestration only.
//
// Adding a new reviewer is one new file under config/documents/
// with `type: skill` and `tags: [kind:reviewer]` in frontmatter.
// Tuning an existing reviewer is editing that file + rebuilding
// the binary; alternatively, an operator with substrate write
// access can edit the skill row via document_upsert.
package reviewer

import (
	"context"
)

// Definition is the parsed shape of one reviewer markdown file:
// frontmatter knobs + raw body to render against the story.
type Definition struct {
	// Name is the registry key. Derived from frontmatter `name:`,
	// not from filename — the filename is incidental.
	Name string

	// Enabled controls whether the orchestrator dispatches against
	// this reviewer. Disabled reviewers load (so operators can flip
	// them on without restart-coordinated config drift) but never
	// run.
	Enabled bool

	// Model is the LLM model identifier passed to Client.Complete.
	// The shape is provider-specific; the orchestrator does not
	// interpret it.
	Model string

	// MaxTokens caps the completion length. Zero means use the
	// client's default.
	MaxTokens int

	// Body is the raw markdown after the frontmatter. The runner
	// renders this verbatim as the LLM prompt, then appends the
	// story JSON below it.
	Body string
}

// Finding is one item the reviewer emits.
type Finding struct {
	Severity string `json:"severity"`
	Code     string `json:"code,omitempty"`
	Field    string `json:"field,omitempty"`
	Message  string `json:"message"`
}

// Output is the expected JSON shape the reviewer returns. The
// runner Unmarshal's into this type before appending findings to
// the ledger.
type Output struct {
	Findings []Finding `json:"findings"`
}

// Client is the LLM transport the runner calls. One Complete call
// = one (prompt → completion text) exchange. The transport is
// expected to handle retries / timeouts at its layer.
type Client interface {
	Complete(ctx context.Context, model string, maxTokens int, prompt string) (string, error)
}
