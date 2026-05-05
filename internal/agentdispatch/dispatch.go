package agentdispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/ternarybob/arbor"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/task"
)

// Config bundles the per-dispatch tunables. Most are sourced from KV
// rows via ResolveConfig; RepoPath + SubstrateMCPURL are caller-
// supplied because they don't have stable defaults (the substrate is
// agnostic about where the operator's repo lives, and the MCP URL is
// deployment-specific).
type Config struct {
	// Mode selects the dispatch backend. Today only "bash" is
	// implemented; non-bash values cause Dispatch to return
	// ErrDispatchModeUnsupported.
	Mode string

	// ClaudePath is the path to the claude binary. Default "claude"
	// (PATH lookup).
	ClaudePath string

	// TimeoutSeconds caps a single dispatch's wall time. Default 600.
	// Enforced via context.WithTimeout — a timeout returns the partial
	// stdout captured before SIGKILL.
	TimeoutSeconds int

	// PreserveWorktreeOnFailure controls cleanup on failure. When true
	// (default) the worktree + branch are kept so the operator can
	// inspect a failed dispatch via `git worktree list`. When false the
	// worktree is removed (`--force`) on failure.
	PreserveWorktreeOnFailure bool

	// RepoPath is the absolute path of the operator's git repo. The
	// worktree lands under <RepoPath>/.satellites-agents/<task_id>.
	// Required.
	RepoPath string

	// SubstrateMCPURL is the URL written into the dispatched session's
	// .claude/mcp.json. May be empty for tests that don't actually
	// connect to MCP — the X-Satellites-Agent header still lands.
	SubstrateMCPURL string

	// HOMEDir overrides the dispatched subprocess's HOME. When empty,
	// Dispatch mints a fresh tmpdir per dispatch so the subprocess
	// never sees the operator's ~/.claude/ memory.
	HOMEDir string
}

// Deps bundles the store interfaces Dispatch reads from. Logger may be
// nil (logs are skipped); Now defaults to time.Now().UTC().
type Deps struct {
	Tasks    task.Store
	Docs     document.Store
	Ledger   ledger.Store
	Stories  storyStoreType
	Projects projectStoreType
	Logger   arbor.ILogger
	Now      func() time.Time
}

// Result is the structured return from Dispatch. Branch + HeadSHA
// describe the worktree's state; EvidenceLedgerID points at the
// kind:dispatch-result ledger row written at the end. Error is
// populated when Success=false.
type Result struct {
	Success          bool   `json:"success"`
	Branch           string `json:"branch,omitempty"`
	HeadSHA          string `json:"head_sha,omitempty"`
	EvidenceLedgerID string `json:"evidence_ledger_id,omitempty"`
	Error            string `json:"error,omitempty"`

	// ClaudeStdout is the raw subprocess stdout (the JSON envelope
	// claude -p emits). Surfaced for tests + audit; not part of the
	// MCP verb's response body.
	ClaudeStdout string `json:"-"`

	// WorktreeDir is the absolute path of the worktree. Surfaced so
	// callers + tests can inspect file artifacts.
	WorktreeDir string `json:"worktree_dir,omitempty"`
}

// Dispatch errors. Callers can check via errors.Is.
var (
	ErrDispatchModeUnsupported  = errors.New("agentdispatch: dispatch mode not supported")
	ErrAgentCannotDeliverAction = errors.New("agentdispatch: agent capability mismatch")
	ErrTaskNotFound             = errors.New("agentdispatch: task not found")
	ErrAgentNotFound            = errors.New("agentdispatch: agent doc not found")
	ErrRepoPathRequired         = errors.New("agentdispatch: cfg.RepoPath required")
	ErrTaskMissingAction        = errors.New("agentdispatch: task missing action — cannot resolve contract or capability")
)

// Dispatch is the substrate's agent-dispatch primitive. The full flow:
//
//  1. Validate mode (must be in SupportedModes).
//  2. Load the task by id; require a non-empty Action.
//  3. Load the agent doc by id; verify capability against the task's
//     Action (CanDeliver for kind=work, CanReview for kind=review).
//  4. Create a git worktree at <RepoPath>/.satellites-agents/<task_id>
//     on a fresh branch agent-<task_id>-from-<short(HEAD)>.
//  5. Write .claude/settings.json (permission_patterns + hooks).
//  6. Write .claude/mcp.json (substrate URL + X-Satellites-Agent
//     header for audit attribution).
//  7. Compose the prompt (six load-bearing context sources per
//     pr_substrate_provides_context).
//  8. Spawn `claude -p <prompt>` with --allowedTools / --mcp-config /
//     --strict-mcp-config / --output-format json under a fresh HOME so
//     the subprocess can't read the operator's ~/.claude/ memory.
//  9. Capture the JSON envelope; parse result/session_id/cost/usage.
//  10. Read the worktree's HEAD sha (commits the agent landed) +
//     compute diff stat against the dispatch-time HEAD.
//  11. Append a kind:dispatch-result ledger row tagged task_id:<id>
//     capturing exit code, duration, cost, agent role, branch name,
//     head sha, diff summary.
//  12. Tear down the worktree per PreserveWorktreeOnFailure on the
//     failure path; always tear down on success.
func Dispatch(ctx context.Context, cfg Config, deps Deps, taskID, agentDocID string) (Result, error) {
	if cfg.RepoPath == "" {
		return Result{}, ErrRepoPathRequired
	}
	mode := cfg.Mode
	if mode == "" {
		mode = DefaultMode
	}
	if !modeSupported(mode) {
		return Result{}, fmt.Errorf("%w: %q (supported: %s)", ErrDispatchModeUnsupported, mode, strings.Join(SupportedModes, ","))
	}
	if cfg.ClaudePath == "" {
		cfg.ClaudePath = DefaultClaudePath
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = DefaultTimeoutSeconds
	}
	now := deps.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}

	if deps.Tasks == nil || deps.Docs == nil || deps.Ledger == nil {
		return Result{}, fmt.Errorf("agentdispatch: deps Tasks/Docs/Ledger required")
	}

	t, err := deps.Tasks.GetByID(ctx, taskID, nil)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %s: %v", ErrTaskNotFound, taskID, err)
	}
	if t.Action == "" {
		return Result{}, fmt.Errorf("%w: task %s", ErrTaskMissingAction, taskID)
	}

	agentDoc, err := deps.Docs.GetByID(ctx, agentDocID, nil)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %s: %v", ErrAgentNotFound, agentDocID, err)
	}
	if agentDoc.Type != document.TypeAgent {
		return Result{}, fmt.Errorf("%w: doc %s is type=%q (want agent)", ErrAgentNotFound, agentDocID, agentDoc.Type)
	}

	settings, err := document.UnmarshalAgentSettings(agentDoc.Structured)
	if err != nil {
		return Result{}, fmt.Errorf("agentdispatch: agent %s: decode settings: %w", agentDocID, err)
	}
	if err := verifyCapability(t, settings); err != nil {
		return Result{}, err
	}

	wp, err := createWorktree(ctx, cfg.RepoPath, taskID)
	if err != nil {
		return Result{}, err
	}
	res := Result{Branch: wp.BranchName, WorktreeDir: wp.WorktreeDir}

	if err := writeSettingsJSON(wp.SettingsPath, settings.PermissionPatterns); err != nil {
		_ = removeWorktree(ctx, cfg.RepoPath, wp, true)
		return res, fmt.Errorf("agentdispatch: write settings.json: %w", err)
	}
	if err := writeMCPConfigJSON(wp.MCPConfigPath, cfg.SubstrateMCPURL, roleFromAgentDoc(agentDoc), taskID); err != nil {
		_ = removeWorktree(ctx, cfg.RepoPath, wp, true)
		return res, fmt.Errorf("agentdispatch: write mcp.json: %w", err)
	}

	prompt := composePrompt(ctx, deps, t, agentDoc)

	homeDir := cfg.HOMEDir
	cleanupHome := false
	if homeDir == "" {
		dir, herr := os.MkdirTemp("", "satellites-agentdispatch-home-*")
		if herr != nil {
			_ = removeWorktree(ctx, cfg.RepoPath, wp, true)
			return res, fmt.Errorf("agentdispatch: mkdir HOME: %w", herr)
		}
		homeDir = dir
		cleanupHome = true
	}
	defer func() {
		if cleanupHome {
			_ = os.RemoveAll(homeDir)
		}
	}()

	subCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.TimeoutSeconds)*time.Second)
	defer cancel()

	startedAt := now()
	stdout, runErr := runClaude(subCtx, cfg, wp, prompt, settings.PermissionPatterns, homeDir)
	finishedAt := now()
	res.ClaudeStdout = stdout
	exitCode := 0
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	envelope := parseClaudeEnvelope(stdout)
	headAfter, _ := gitOutput(ctx, cfg.RepoPath, "rev-parse", wp.BranchName)
	headAfter = strings.TrimSpace(headAfter)
	res.HeadSHA = headAfter
	diffStat, _ := gitOutput(ctx, cfg.RepoPath, "diff", "--stat", wp.HeadShort+".."+wp.BranchName)
	diffStat = strings.TrimSpace(diffStat)

	res.Success = runErr == nil && exitCode == 0
	if !res.Success {
		if runErr != nil {
			res.Error = runErr.Error()
		} else {
			res.Error = fmt.Sprintf("claude exited with code %d", exitCode)
		}
	}

	ledgerID, lerr := writeDispatchLedger(ctx, deps, t, agentDoc, wp, res, envelope, exitCode, startedAt, finishedAt, diffStat, now())
	if lerr != nil && deps.Logger != nil {
		deps.Logger.Warn().Str("task_id", taskID).Str("error", lerr.Error()).Msg("agentdispatch: ledger append failed")
	}
	res.EvidenceLedgerID = ledgerID

	switch {
	case res.Success:
		// Successful dispatch: tear down worktree (branch ref kept for
		// inspection / merge by the orchestrator). Use --force because
		// the substrate's own .claude/settings.json + .claude/mcp.json
		// land as untracked files in the worktree; without --force
		// `git worktree remove` refuses to clean.
		if rerr := removeWorktree(ctx, cfg.RepoPath, wp, true); rerr != nil && deps.Logger != nil {
			deps.Logger.Warn().Str("task_id", taskID).Str("error", rerr.Error()).Msg("agentdispatch: worktree teardown failed")
		}
	case cfg.PreserveWorktreeOnFailure:
		if deps.Logger != nil {
			deps.Logger.Info().Str("task_id", taskID).Str("worktree", wp.WorktreeDir).Msg("agentdispatch: failure path — worktree preserved for inspection")
		}
	default:
		if rerr := removeWorktree(ctx, cfg.RepoPath, wp, true); rerr != nil && deps.Logger != nil {
			deps.Logger.Warn().Str("task_id", taskID).Str("error", rerr.Error()).Msg("agentdispatch: worktree teardown failed")
		}
	}

	if deps.Logger != nil {
		deps.Logger.Info().
			Str("task_id", taskID).
			Str("agent_doc_id", agentDocID).
			Str("agent_role", agentDoc.Name).
			Str("branch", wp.BranchName).
			Str("head_sha", headAfter).
			Bool("success", res.Success).
			Int("exit_code", exitCode).
			Int64("duration_ms", finishedAt.Sub(startedAt).Milliseconds()).
			Float64("cost_usd", envelope.CostUSD).
			Msg("agentdispatch: dispatch complete")
	}

	return res, nil
}

// verifyCapability cross-checks the task's Action against the agent's
// declared delivers / reviews lists. kind=review tasks must match a
// reviews entry; everything else (kind=work or empty) must match a
// delivers entry.
func verifyCapability(t task.Task, settings document.AgentSettings) error {
	if t.Kind == task.KindReview {
		if !settings.CanReview(t.Action) {
			return fmt.Errorf("%w: agent does not review %q (reviews=%v)", ErrAgentCannotDeliverAction, t.Action, settings.Reviews)
		}
		return nil
	}
	if !settings.CanDeliver(t.Action) {
		return fmt.Errorf("%w: agent does not deliver %q (delivers=%v)", ErrAgentCannotDeliverAction, t.Action, settings.Delivers)
	}
	return nil
}

// runClaude spawns the claude subprocess with the dispatch flags and
// returns its stdout. Stderr is captured into the returned error on
// non-zero exit.
func runClaude(ctx context.Context, cfg Config, wp worktreePaths, prompt string, permissionPatterns []string, homeDir string) (string, error) {
	args := []string{
		"-p", prompt,
		"--output-format", "json",
		"--mcp-config", wp.MCPConfigPath,
		"--strict-mcp-config",
	}
	if len(permissionPatterns) > 0 {
		args = append(args, "--allowedTools", strings.Join(permissionPatterns, ","))
	}
	cmd := exec.CommandContext(ctx, cfg.ClaudePath, args...)
	cmd.Dir = wp.WorktreeDir
	cmd.Env = append(os.Environ(),
		"HOME="+homeDir,
		"SATELLITES_AGENT_TASK_ID="+strings.TrimPrefix(wp.WorktreeDir, ""), // diagnostic
	)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return string(out), fmt.Errorf("claude exit %d: %s", exitErr.ExitCode(), strings.TrimSpace(string(exitErr.Stderr)))
		}
		return string(out), err
	}
	return string(out), nil
}

// claudeEnvelope is the subset of `claude -p --output-format json` we
// extract for ledger evidence. Unknown fields are ignored.
type claudeEnvelope struct {
	Result    string         `json:"result"`
	SessionID string         `json:"session_id"`
	CostUSD   float64        `json:"total_cost_usd"`
	Usage     map[string]any `json:"usage"`
}

// parseClaudeEnvelope tolerates a missing/garbled subprocess stdout —
// the dispatch is still recorded; the envelope just goes empty so
// downstream callers can tell.
func parseClaudeEnvelope(stdout string) claudeEnvelope {
	var env claudeEnvelope
	stdout = strings.TrimSpace(stdout)
	if stdout == "" {
		return env
	}
	_ = json.Unmarshal([]byte(stdout), &env)
	return env
}

// writeDispatchLedger appends a kind:dispatch-result ledger row tagged
// task_id:<id> + agent_role:<role>. Returns the row id (empty when the
// append failed).
func writeDispatchLedger(ctx context.Context, deps Deps, t task.Task, agent document.Document, wp worktreePaths, res Result, env claudeEnvelope, exitCode int, startedAt, finishedAt time.Time, diffStat string, now time.Time) (string, error) {
	role := roleFromAgentDoc(agent)
	tags := []string{
		"kind:dispatch-result",
		"task_id:" + t.ID,
		"agent_role:" + role,
	}
	if t.StoryID != "" {
		tags = append(tags, "story_id:"+t.StoryID)
	}
	structured, _ := json.Marshal(map[string]any{
		"success":         res.Success,
		"exit_code":       exitCode,
		"branch":          wp.BranchName,
		"head_sha_before": wp.HeadShort,
		"head_sha_after":  res.HeadSHA,
		"agent_doc_id":    agent.ID,
		"agent_role":      role,
		"task_id":         t.ID,
		"task_action":     t.Action,
		"task_kind":       t.Kind,
		"started_at":      startedAt.Format(time.RFC3339Nano),
		"finished_at":     finishedAt.Format(time.RFC3339Nano),
		"duration_ms":     finishedAt.Sub(startedAt).Milliseconds(),
		"cost_usd":        env.CostUSD,
		"session_id":      env.SessionID,
		"usage":           env.Usage,
		"diff_stat":       diffStat,
		"error":           res.Error,
		"worktree":        wp.WorktreeDir,
	})
	content := fmt.Sprintf("dispatch %s for task %s (action=%s) — success=%v exit=%d duration=%dms",
		role, t.ID, t.Action, res.Success, exitCode, finishedAt.Sub(startedAt).Milliseconds())
	storyPtr := (*string)(nil)
	if t.StoryID != "" {
		s := t.StoryID
		storyPtr = &s
	}
	entry := ledger.LedgerEntry{
		WorkspaceID: t.WorkspaceID,
		ProjectID:   t.ProjectID,
		StoryID:     storyPtr,
		Type:        ledger.TypeDecision,
		Tags:        tags,
		Content:     content,
		Structured:  structured,
		Durability:  ledger.DurabilityDurable,
		SourceType:  ledger.SourceSystem,
		Status:      ledger.StatusActive,
		CreatedBy:   "system",
	}
	row, err := deps.Ledger.Append(ctx, entry, now)
	if err != nil {
		return "", err
	}
	return row.ID, nil
}
