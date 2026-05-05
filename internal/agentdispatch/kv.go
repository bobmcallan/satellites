// Package agentdispatch is the substrate primitive for orchestrator-
// driven external agent dispatch (sty_51571015). The Dispatch function
// loads an agent doc + a task, verifies capability, creates an
// isolated git worktree under `<repo>/.satellites-agents/<task_id>`,
// writes the worktree's `.claude/settings.json` + `.claude/mcp.json`
// (carrying the substrate's `X-Satellites-Agent` header for audit
// attribution), composes a six-source context bundle (agent_process
// artifact + agent doc + active principles + story_context + contract
// body + task chain), and spawns `claude -p` as a subprocess with the
// agent's permission_patterns enforced via `--allowedTools` and a
// scratch HOME so the dispatched session never sees the operator's
// `~/.claude/` memory directory.
//
// Per `pr_substrate_provides_context`: the substrate is the
// authoritative source of context for dispatched agents — operator-
// side Claude Code memory is orchestrator-only.
//
// Replaces `internal/reviewer/service` (the in-process listener that
// claimed kind:review tasks and wrote verdicts directly). After this
// story lands, every review is dispatched the same way every work
// task is — through `agent_dispatch`.
package agentdispatch

import (
	"context"
	"strconv"
	"strings"

	"github.com/bobmcallan/satellites/internal/ledger"
)

// KV keys consumed by Dispatch. Read at the start of every dispatch
// via ledger.KVResolveScoped (system-tier resolution; operators flip
// values via kv_set at scope=system). Defaults apply when the row is
// missing.
const (
	// KVKeyMode selects the dispatch backend. Today only "bash"
	// (claude -p subprocess) is implemented; future modes ("team",
	// "subagent") are forward-compat slots and return
	// ErrDispatchModeUnsupported.
	KVKeyMode = "agent.dispatch.mode"

	// KVKeyClaudePath is the path to the claude binary. PATH lookup
	// when empty.
	KVKeyClaudePath = "agent.dispatch.bash.claude_path"

	// KVKeyTimeoutSeconds caps a single dispatch's wall time.
	KVKeyTimeoutSeconds = "agent.dispatch.bash.timeout_seconds"

	// KVKeyPreserveWorktreeOnFailure controls cleanup on failure. When
	// true (default) the worktree + branch are kept so the operator
	// can inspect a failed dispatch; when false the worktree is
	// removed alongside the branch.
	KVKeyPreserveWorktreeOnFailure = "agent.dispatch.bash.preserve_worktree_on_failure"
)

// Defaults applied when the corresponding KV row is missing.
const (
	DefaultMode                      = ModeBash
	DefaultClaudePath                = "claude"
	DefaultTimeoutSeconds            = 600
	DefaultPreserveWorktreeOnFailure = true
)

// Mode enum values. Bash is the only implemented mode; the others are
// forward-compat slots so the substrate rejects them with a typed
// error rather than silently falling back.
const (
	ModeBash     = "bash"
	ModeTeam     = "team"     // reserved
	ModeSubagent = "subagent" // reserved
)

// SupportedModes lists the modes Dispatch accepts. Returned in
// ErrDispatchModeUnsupported messages so operators see what's
// permitted.
var SupportedModes = []string{ModeBash}

// ResolveConfig reads the four dispatch KV rows from the ledger and
// returns a Config populated with values + defaults. workspaceID and
// projectID may be empty — the system tier still resolves; project /
// user tiers are only consulted when the corresponding ID is non-empty.
//
// Failures on individual KV reads fall back to the default for that
// key; the only non-recoverable error is a non-empty Mode value that
// isn't in SupportedModes, which is reported by Dispatch (not here)
// so the caller's own KV-read errors don't conflate with mode-policy
// errors.
func ResolveConfig(ctx context.Context, store ledger.Store, opts ledger.KVResolveOptions, memberships []string) Config {
	cfg := Config{
		Mode:                      DefaultMode,
		ClaudePath:                DefaultClaudePath,
		TimeoutSeconds:            DefaultTimeoutSeconds,
		PreserveWorktreeOnFailure: DefaultPreserveWorktreeOnFailure,
	}
	if store == nil {
		return cfg
	}
	if v, ok := readKVString(ctx, store, KVKeyMode, opts, memberships); ok {
		cfg.Mode = v
	}
	if v, ok := readKVString(ctx, store, KVKeyClaudePath, opts, memberships); ok {
		cfg.ClaudePath = v
	}
	if v, ok := readKVString(ctx, store, KVKeyTimeoutSeconds, opts, memberships); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			cfg.TimeoutSeconds = n
		}
	}
	if v, ok := readKVString(ctx, store, KVKeyPreserveWorktreeOnFailure, opts, memberships); ok {
		cfg.PreserveWorktreeOnFailure = parseBool(v, DefaultPreserveWorktreeOnFailure)
	}
	return cfg
}

func readKVString(ctx context.Context, store ledger.Store, key string, opts ledger.KVResolveOptions, memberships []string) (string, bool) {
	row, found, err := ledger.KVResolveScoped(ctx, store, key, opts, memberships)
	if err != nil || !found {
		return "", false
	}
	v := strings.TrimSpace(row.Value)
	if v == "" {
		return "", false
	}
	return v, true
}

func parseBool(s string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	default:
		return fallback
	}
}

// modeSupported reports whether mode is in SupportedModes.
func modeSupported(mode string) bool {
	for _, m := range SupportedModes {
		if m == mode {
			return true
		}
	}
	return false
}
