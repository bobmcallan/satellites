// Per-transition step summariser (sty_2517f6b8).
//
// After a gated transition is enacted, the loop runs a config-named
// summariser skill to produce a concise, human-readable summary of the
// step. Unlike a gate it returns PROSE, not a {decision} object: the loop
// records its stdout verbatim as a `step_summary` ledger row. The skill is
// configuration (project-config `step_summariser_skill`); the invocation +
// ledger write + portal render are code. Reuses the gate dispatcher's skill
// resolution (resolveGateSkillBody) so a workflow/gate and a summariser are
// the same kind of artifact — a SKILL.md in the worktree.

package verb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/ledger"
)

// SummariserInput is what the summariser receives: the skill to run and the
// transition to describe (story body + from/to + decision + recent ledger).
// ReviewerKey rides along so the subprocess authenticates back as the
// reviewer if the skill itself reads the substrate, matching the gate path.
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
	ReviewerKey  string
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
	// DefaultTimeout caps a run when SummariserInput.Timeout is zero.
	DefaultTimeout time.Duration
}

// summariserAllowedTools is the read-only grant the summariser subprocess
// runs under. A step summary inspects the story + tree to describe what the
// transition did; it must not build or edit, so Bash is withheld (unlike the
// gate, which needs it to run tests). Space-separated per --allowedTools.
const summariserAllowedTools = "Read Grep Glob"

// summariserClaudeArgs builds the claude argv for a summariser run. The skill
// body is the appended system prompt; the transition payload arrives on
// stdin. Standalone so a test can pin the argv (and catch a future invalid
// flag or a widened tool grant) without a live run.
func summariserClaudeArgs(systemPrompt string) []string {
	return []string{
		"-p",
		"--allowedTools", summariserAllowedTools,
		"--append-system-prompt", systemPrompt,
	}
}

// Summarise runs the skill and returns its stdout prose, trimmed. Errors are
// the caller's to treat as non-fatal: a missing summary is observability
// lost, not a transition undone.
func (c ClaudeCLISummariser) Summarise(ctx context.Context, in SummariserInput) (string, error) {
	if strings.TrimSpace(in.SkillName) == "" {
		return "", fmt.Errorf("summariser: skill_name required")
	}
	binary := c.BinaryPath
	if binary == "" {
		binary = "claude"
	}
	timeout := in.Timeout
	if timeout == 0 {
		timeout = c.DefaultTimeout
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

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

	systemPrompt, err := resolveGateSkillBody(in.WorktreeRoot, in.SkillName)
	if err != nil {
		return "", err
	}

	cmd := exec.CommandContext(ctx, binary, summariserClaudeArgs(systemPrompt)...)
	cmd.Stdin = strings.NewReader(string(payload))
	if in.WorktreeRoot != "" {
		cmd.Dir = in.WorktreeRoot
	}
	cmd.Env = os.Environ()
	if strings.TrimSpace(in.ReviewerKey) != "" {
		cmd.Env = append(cmd.Env, "SATELLITES_REVIEWER_API_KEY="+in.ReviewerKey)
	}

	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", fmt.Errorf("summariser: subprocess exited: %s: %s",
				exitErr.String(), strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("summariser: subprocess: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
