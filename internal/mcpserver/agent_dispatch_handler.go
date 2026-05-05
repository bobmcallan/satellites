// Package mcpserver — agent_dispatch verb (sty_51571015).
//
// agent_dispatch is the substrate primitive for orchestrator-driven
// external agent execution. The orchestrator (operator's Claude Code
// session) calls this verb with (task_id, agent_doc_id, repo_path);
// the substrate composes the dispatched agent's full context bundle
// (per pr_substrate_provides_context), creates an isolated git
// worktree, writes per-agent .claude/settings.json + .claude/mcp.json
// (carrying X-Satellites-Agent for audit attribution), and spawns
// `claude -p` with the agent's permission_patterns enforced via
// --allowedTools. The dispatched session runs under a fresh HOME so
// it never sees the operator's ~/.claude/ memory directory.
//
// Implementation lives in internal/agentdispatch.Dispatch; this file
// is the MCP-side glue + KV resolution for the four
// `agent.dispatch.*` switches.
package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/bobmcallan/satellites/internal/agentdispatch"
	"github.com/bobmcallan/satellites/internal/ledger"
)

// handleAgentDispatch implements the agent_dispatch MCP verb.
func (s *Server) handleAgentDispatch(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	start := time.Now()
	caller, _ := UserFrom(ctx)

	taskID, err := req.RequireString("task_id")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	agentDocID, err := req.RequireString("agent_doc_id")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	repoPath, err := req.RequireString("repo_path")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}

	if s.tasks == nil || s.docs == nil || s.ledger == nil {
		return mcpgo.NewToolResultError("agent_dispatch unavailable: tasks/docs/ledger store not configured"), nil
	}

	// Resolve KV configuration. The system tier is the substrate-managed
	// source of truth; operators flip values via kv_set at scope=system.
	memberships := s.resolveCallerMemberships(ctx, caller)
	memberships = append([]string{""}, memberships...)
	cfg := agentdispatch.ResolveConfig(ctx, s.ledger, ledger.KVResolveOptions{}, memberships)
	cfg.RepoPath = repoPath
	if mcpURL := req.GetString("mcp_url", ""); mcpURL != "" {
		cfg.SubstrateMCPURL = mcpURL
	} else if s.cfg != nil && s.cfg.PublicURL != "" {
		cfg.SubstrateMCPURL = strings.TrimRight(s.cfg.PublicURL, "/") + "/mcp"
	}

	deps := agentdispatch.Deps{
		Tasks:    s.tasks,
		Docs:     s.docs,
		Ledger:   s.ledger,
		Stories:  s.stories,
		Projects: s.projects,
		Logger:   s.logger,
		Now:      s.nowFunc,
	}

	res, derr := agentdispatch.Dispatch(ctx, cfg, deps, taskID, agentDocID)
	if derr != nil {
		s.logger.Warn().
			Str("tool", "agent_dispatch").
			Str("task_id", taskID).
			Str("agent_doc_id", agentDocID).
			Str("error", derr.Error()).
			Msg("agent_dispatch failed before subprocess")
		return mcpgo.NewToolResultError(derr.Error()), nil
	}

	body, _ := json.Marshal(map[string]any{
		"success":            res.Success,
		"branch":             res.Branch,
		"head_sha":           res.HeadSHA,
		"evidence_ledger_id": res.EvidenceLedgerID,
		"worktree_dir":       res.WorktreeDir,
		"error":              res.Error,
	})
	s.logger.Info().
		Str("method", "tools/call").
		Str("tool", "agent_dispatch").
		Str("task_id", taskID).
		Str("agent_doc_id", agentDocID).
		Bool("success", res.Success).
		Str("branch", res.Branch).
		Int64("duration_ms", time.Since(start).Milliseconds()).
		Msg("mcp tool call")
	return mcpgo.NewToolResultText(string(body)), nil
}
