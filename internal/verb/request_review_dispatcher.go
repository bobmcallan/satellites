// Gate-skill dispatcher seam for story_request_review.
//
// The verb composes a gate run as: read story → resolve workflow skill →
// pick transition → mint reviewer key → invoke gate skill → parse decision.
// The invocation step is the only piece that talks to the outside world
// (a `claude -p` subprocess in prod, a stub in tests), so it lives behind
// this interface. Tests inject a fake; the server boot wires the prod
// `claude -p` dispatcher.

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

// GateInput is what the dispatcher receives. The verb hands over the
// minimum a gate skill needs to make its call: which skill to dispatch,
// the story body + recent ledger to feed it on stdin, and the reviewer
// key the subprocess wields when patching back through verbs.
type GateInput struct {
	SkillName    string
	StoryID      string
	StoryBody    string
	StoryStatus  string
	NextStatus   string
	Dynamic      bool
	RecentLedger []ledger.Entry
	ReviewerKey  string
	WorktreeRoot string
	Timeout      time.Duration
}

// GateOutput is what the dispatcher returns. NextStatus is honoured only
// when the input had Dynamic=true — declarative dispatchers can leave it
// empty and the verb fills it from the workflow's `to`.
type GateOutput struct {
	Decision   string `json:"decision"`
	Notes      string `json:"notes,omitempty"`
	NextStatus string `json:"next_status,omitempty"`
}

// GateDispatcher is the indirection seam. One method, one call — keeps
// the contract narrow so test fakes are obvious and the prod impl stays
// small.
type GateDispatcher interface {
	Dispatch(ctx context.Context, in GateInput) (GateOutput, error)
}

// GateDispatcherFunc adapts an ordinary function to the GateDispatcher
// interface — handy for test injection.
type GateDispatcherFunc func(ctx context.Context, in GateInput) (GateOutput, error)

// Dispatch satisfies GateDispatcher.
func (f GateDispatcherFunc) Dispatch(ctx context.Context, in GateInput) (GateOutput, error) {
	return f(ctx, in)
}

var gateDispatcher GateDispatcher

// SetGateDispatcher wires the gate-skill dispatcher into the verb
// package. Pass nil to disable dispatch (story_request_review returns
// "gate dispatcher not configured"); tests pass a stub between cases.
func SetGateDispatcher(d GateDispatcher) { gateDispatcher = d }

// ClaudeCLIGateDispatcher invokes `claude -p --skill <name>` as a
// subprocess in WorktreeRoot. The story body + recent ledger arrive on
// stdin; the reviewer key arrives in env as SATELLITES_REVIEWER_API_KEY
// so the skill can authenticate back through the verbs.
//
// The skill is expected to print one JSON object: `{decision, notes,
// next_status?}`. Anything else is a dispatcher-level error — the
// substrate refuses to interpret malformed gate output.
type ClaudeCLIGateDispatcher struct {
	// BinaryPath overrides the discovered `claude` binary. Production
	// callers leave it empty; tests that exercise the subprocess path
	// without a real install point this at a shim.
	BinaryPath string

	// DefaultTimeout caps a single dispatch when GateInput.Timeout is
	// zero. Zero here disables the cap entirely — used by tests that
	// want fine-grained control.
	DefaultTimeout time.Duration
}

// Dispatch satisfies GateDispatcher with a `claude -p` subprocess. Story
// 11 (`audit-prose + size-budget`) layers an additional gate runner on
// top of this surface; for now `claude` is the only built-in transport.
func (c ClaudeCLIGateDispatcher) Dispatch(ctx context.Context, in GateInput) (GateOutput, error) {
	if strings.TrimSpace(in.SkillName) == "" {
		return GateOutput{}, fmt.Errorf("gate dispatch: skill_name required")
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
		"story_body":    in.StoryBody,
		"story_status":  in.StoryStatus,
		"next_status":   in.NextStatus,
		"dynamic":       in.Dynamic,
		"recent_ledger": in.RecentLedger,
	})
	if err != nil {
		return GateOutput{}, fmt.Errorf("gate dispatch: marshal payload: %w", err)
	}

	cmd := exec.CommandContext(ctx, binary, "-p", "--skill", in.SkillName)
	cmd.Stdin = strings.NewReader(string(payload))
	if in.WorktreeRoot != "" {
		cmd.Dir = in.WorktreeRoot
	}
	// Inherit the caller's environment — claude needs PATH/HOME and the
	// operator's auth to run at all. The reviewer key, when minted, is
	// layered on top so the subprocess authenticates back as the
	// reviewer; an empty key leaves the inherited operator auth in place
	// (the client-side gate path mints no key).
	cmd.Env = os.Environ()
	if strings.TrimSpace(in.ReviewerKey) != "" {
		cmd.Env = append(cmd.Env, "SATELLITES_REVIEWER_API_KEY="+in.ReviewerKey)
	}

	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return GateOutput{}, fmt.Errorf("gate dispatch: subprocess exited: %s: %s",
				exitErr.String(), strings.TrimSpace(string(exitErr.Stderr)))
		}
		return GateOutput{}, fmt.Errorf("gate dispatch: subprocess: %w", err)
	}
	return ParseGateOutput(out)
}

// ParseGateOutput is lenient on whitespace + leading prose but strict on
// shape — a missing `decision` or one outside {accept, reject} is a
// dispatcher-level error. Mirrors the existing reviewer/runner.go
// parseOutput tolerance. Exported so the client-side reviewer command
// parses gate output through the same owner.
func ParseGateOutput(raw []byte) (GateOutput, error) {
	s := strings.TrimSpace(string(raw))
	if strings.HasPrefix(s, "```") {
		if nl := strings.IndexByte(s, '\n'); nl >= 0 {
			s = s[nl+1:]
		}
		if end := strings.LastIndex(s, "```"); end >= 0 {
			s = s[:end]
		}
		s = strings.TrimSpace(s)
	}
	if i := strings.IndexByte(s, '{'); i > 0 {
		s = s[i:]
	}
	var out GateOutput
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return GateOutput{}, fmt.Errorf("gate dispatch: parse output: %w (raw=%q)", err, raw)
	}
	switch out.Decision {
	case GateDecisionAccept, GateDecisionReject:
	default:
		return GateOutput{}, fmt.Errorf("gate dispatch: invalid decision %q (want accept|reject)", out.Decision)
	}
	return out, nil
}

// Gate decision discriminators emitted by gate skills and recorded in
// the ledger as the canonical accept/reject signal.
const (
	GateDecisionAccept = "accept"
	GateDecisionReject = "reject"
)
