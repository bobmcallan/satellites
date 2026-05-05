package agentdispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// worktreeRootDir is the top-level directory under the operator's repo
// where every dispatched agent's worktree lands. The directory is in
// .gitignore so the operator's git status stays clean.
const worktreeRootDir = ".satellites-agents"

// worktreePaths bundles the absolute paths of one dispatch's worktree
// + its config files so callers don't restitch them.
type worktreePaths struct {
	WorktreeDir   string // <repo>/.satellites-agents/<task_id>
	BranchName    string // agent-<task_id>-from-<short(HEAD)>
	HeadShort     string // short HEAD sha at dispatch time
	SettingsPath  string // <worktree>/.claude/settings.json
	MCPConfigPath string // <worktree>/.claude/mcp.json
}

// createWorktree provisions an isolated git worktree under
// `<repoPath>/.satellites-agents/<taskID>` on a fresh branch
// `agent-<taskID>-from-<short(HEAD)>`. The worktree's HEAD starts at
// the same commit the operator's repo is on. Returns the populated
// paths struct or an error.
func createWorktree(ctx context.Context, repoPath, taskID string) (worktreePaths, error) {
	wp := worktreePaths{}

	repoAbs, err := filepath.Abs(repoPath)
	if err != nil {
		return wp, fmt.Errorf("agentdispatch: resolve repo path %q: %w", repoPath, err)
	}
	if st, err := os.Stat(filepath.Join(repoAbs, ".git")); err != nil || !(st.IsDir() || st.Mode().IsRegular()) {
		return wp, fmt.Errorf("agentdispatch: %s is not a git repo (no .git entry)", repoAbs)
	}

	headShort, err := gitOutput(ctx, repoAbs, "rev-parse", "--short", "HEAD")
	if err != nil {
		return wp, fmt.Errorf("agentdispatch: rev-parse HEAD failed: %w", err)
	}
	headShort = strings.TrimSpace(headShort)
	wp.HeadShort = headShort

	wp.BranchName = fmt.Sprintf("agent-%s-from-%s", taskID, headShort)
	wp.WorktreeDir = filepath.Join(repoAbs, worktreeRootDir, taskID)

	if err := os.MkdirAll(filepath.Dir(wp.WorktreeDir), 0o755); err != nil {
		return wp, fmt.Errorf("agentdispatch: mkdir worktree parent: %w", err)
	}

	// Pre-clean: a prior failed dispatch with preserve=false may have
	// left the directory; a prior success with preserve=true will have
	// kept it. Either way, refuse to overwrite — the operator must
	// remove a stale worktree explicitly.
	if _, err := os.Stat(wp.WorktreeDir); err == nil {
		return wp, fmt.Errorf("agentdispatch: worktree %s already exists (operator: git worktree remove %s)", wp.WorktreeDir, wp.WorktreeDir)
	}

	if _, err := gitOutput(ctx, repoAbs, "worktree", "add", "-b", wp.BranchName, wp.WorktreeDir, "HEAD"); err != nil {
		return wp, fmt.Errorf("agentdispatch: git worktree add failed: %w", err)
	}

	claudeDir := filepath.Join(wp.WorktreeDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		return wp, fmt.Errorf("agentdispatch: mkdir .claude: %w", err)
	}
	wp.SettingsPath = filepath.Join(claudeDir, "settings.json")
	wp.MCPConfigPath = filepath.Join(claudeDir, "mcp.json")

	return wp, nil
}

// removeWorktree tears down the worktree at wp.WorktreeDir using
// `git worktree remove`. The branch ref is preserved by default
// (callers can delete it explicitly). When force=true the worktree is
// removed even when it has uncommitted state — used on failure paths
// where partial changes should not block cleanup.
func removeWorktree(ctx context.Context, repoPath string, wp worktreePaths, force bool) error {
	if wp.WorktreeDir == "" {
		return nil
	}
	repoAbs, err := filepath.Abs(repoPath)
	if err != nil {
		return err
	}
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, wp.WorktreeDir)
	if _, err := gitOutput(ctx, repoAbs, args...); err != nil {
		return fmt.Errorf("agentdispatch: git worktree remove %s: %w", wp.WorktreeDir, err)
	}
	return nil
}

// writeSettingsJSON writes the dispatched agent's `.claude/settings.json`.
// Carries the agent's permission_patterns into both `permissions.allow`
// (Claude Code's native enforcement) and a no-op `PreToolUse` hook
// surface (defence-in-depth: a future hook script can read the patterns
// and audit-log denials). The Stop hook captures the dispatched
// session's exit so the substrate can correlate the JSON result with
// the subprocess lifecycle.
//
// The `--allowedTools` flag passed to claude is the load-bearing
// enforcement; this file backs it up + lets future operators add
// real hook scripts without re-deploying the substrate.
func writeSettingsJSON(path string, permissionPatterns []string) error {
	settings := map[string]any{
		"permissions": map[string]any{
			"allow": permissionPatterns,
		},
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": "*",
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": "/bin/true",
						},
					},
				},
			},
			"Stop": []any{
				map[string]any{
					"matcher": "*",
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": "/bin/true",
						},
					},
				},
			},
		},
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// writeMCPConfigJSON writes the dispatched agent's `.claude/mcp.json`
// with the substrate MCP server pinned + the `X-Satellites-Agent`
// header carrying the agent role + task id so the substrate's MCP
// audit feed can attribute every call back to its dispatched origin.
//
// substrateMCPURL may be empty — in which case the file lists the
// header but no URL, and the dispatched session falls back to whatever
// MCP servers its --strict-mcp-config flag permits (none, since strict
// rejects unconfigured servers).
func writeMCPConfigJSON(path, substrateMCPURL, role, taskID string) error {
	header := map[string]any{
		"X-Satellites-Agent": fmt.Sprintf("%s:%s", role, taskID),
	}
	server := map[string]any{
		"type":    "http",
		"url":     substrateMCPURL,
		"headers": header,
	}
	cfg := map[string]any{
		"mcpServers": map[string]any{
			"satellites": server,
		},
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// gitOutput runs `git -C <repoPath> <args...>` and returns its stdout.
// stderr is folded into the error message on non-zero exit so callers
// see why git refused.
func gitOutput(ctx context.Context, repoPath string, args ...string) (string, error) {
	allArgs := append([]string{"-C", repoPath}, args...)
	cmd := exec.CommandContext(ctx, "git", allArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
