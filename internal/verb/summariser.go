// Per-transition step summariser (sty_2517f6b8).
//
// After a gated transition is enacted, the loop runs a config-named
// summariser skill to produce a concise, human-readable summary of the
// step. Unlike a gate it returns PROSE, not a {decision} object: the loop
// records its stdout verbatim as a `step_summary` ledger row. The skill is
// configuration (project-config `step_summariser_skill`); the invocation +
// ledger write + portal render are code. Reuses the gate dispatcher's skill
// resolution (resolveSkillEmbedLocalServer: embed → local → server) so a
// workflow/gate and a summariser are the same kind of artifact and a summariser
// absent from .claude/skills resolves from the server like a gate.

package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/ledger"
)

// SummariserInput is what the summariser receives: the skill to run and the
// transition to describe (story body + from/to + decision + recent ledger).
type SummariserInput struct {
	SkillName    string
	StoryID      string
	ProjectID    string
	WorkspaceID  string
	StoryBody    string
	FromStatus   string
	ToStatus     string
	Decision     string
	RecentLedger []ledger.Entry
	WorktreeRoot string
	Timeout      time.Duration
}

// ClaudeCLISummariser runs the summariser skill as `claude -p
// --append-system-prompt <skill-body>` in WorktreeRoot, mirroring
// ClaudeCLIGateDispatcher but returning the trimmed stdout prose rather than
// a parsed decision. A summary never builds or mutates the tree, so its tool
// grant is read-only (summariserAllowedTools) — narrower than the gate's.
type ClaudeCLISummariser struct {
	// BinaryPath overrides the discovered `claude` binary; empty = `claude`.
	BinaryPath string
	// Model, when set, rides the argv as `--model <value>` (satellites.toml
	// reviewer_model, sty_c7a5d741). Empty = inherit the harness default.
	Model string
	// DefaultTimeout caps a run when SummariserInput.Timeout is zero.
	DefaultTimeout time.Duration
	// Fetch is the server step of embed → local → server skill resolution
	// (sty_b8de4776), shared with the gate dispatcher: a summariser skill absent
	// from .claude/skills is fetched by name and injected, so the summariser
	// needs no local install. Nil keeps embed → local only (fail-closed on miss).
	Fetch GateBodyFetcher
}

// summariserAllowedTools is the read-only grant the summariser subprocess
// runs under. A step summary inspects the story + tree to describe what the
// transition did; it must not build or edit, so Bash is withheld (unlike the
// gate, which needs it to run tests). Space-separated per --allowedTools.
const summariserAllowedTools = "Read Grep Glob"

// Summarise runs the skill and returns its stdout prose, trimmed. It threads
// through the same single claude -p primitive as the gate (claudeArgs +
// runClaudeCLI, sty_3436e9f0) — only the tool grant (read-only), the stdin
// payload, and the output handling (raw trimmed prose, not a parsed decision)
// differ. Errors are the caller's to treat as non-fatal: a missing summary is
// observability lost, not a transition undone.
func (c ClaudeCLISummariser) Summarise(ctx context.Context, in SummariserInput) (string, error) {
	if strings.TrimSpace(in.SkillName) == "" {
		return "", fmt.Errorf("summariser: skill_name required")
	}
	ctx, cancel := withClaudeTimeout(ctx, in.Timeout, c.DefaultTimeout)
	defer cancel()

	payload, err := json.Marshal(map[string]any{
		"story_id":      in.StoryID,
		"project_id":    in.ProjectID,
		"workspace_id":  in.WorkspaceID,
		"story_body":    in.StoryBody,
		"from_status":   in.FromStatus,
		"to_status":     in.ToStatus,
		"decision":      in.Decision,
		"recent_ledger": in.RecentLedger,
	})
	if err != nil {
		return "", fmt.Errorf("summariser: marshal payload: %w", err)
	}

	_, systemPrompt, err := resolveSkillEmbedLocalServer(ctx, c.Fetch, in.WorktreeRoot, in.SkillName)
	if err != nil {
		return "", err
	}

	out, err := runClaudeCLI(ctx, c.BinaryPath,
		claudeArgs(systemPrompt, summariserAllowedTools, c.Model),
		string(payload), in.WorktreeRoot, "summariser")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
