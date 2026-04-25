// Package mechanical is the satellites-v4 deterministic-agent fallback
// tier per doc_adfa7adf v0.2.0 §2 Agent subsection. When an MCP caller
// invokes agent_role_claim and either (a) no agent-document resolves
// for the requested role, (b) the resolved agent's provider_chain is
// exhausted / empty, or (c) the caller explicitly forces the fallback
// via provider_override="mechanical", the server runs the contract's
// agent_instruction deterministically and tags every produced ledger
// row `provider:mechanical`.
//
// This package is the runner. It produces evidence rows with the same
// shape a full LLM-backed agent run would — so downstream reviewers
// treat mechanical and LLM-backed evidence uniformly. Mirrors the
// reviewer's already-shipped mechanical fallback (story_73b3d1c5).
package mechanical

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bobmcallan/satellites/internal/ledger"
)

// Trigger is the reason the runner was invoked. Values are low-cardinality
// so ledger queries can aggregate.
type Trigger string

const (
	TriggerNoAgent   Trigger = "no-agent-resolved"
	TriggerExhausted Trigger = "provider-exhausted"
	TriggerForceFlag Trigger = "explicit-force"
)

var validTriggers = map[Trigger]struct{}{
	TriggerNoAgent:   {},
	TriggerExhausted: {},
	TriggerForceFlag: {},
}

// Result is the return value from Runner.Run. The caller uses it to
// synthesize an agent_role_claim response without a live LLM provider
// having participated.
type Result struct {
	Trigger        Trigger
	RoleID         string
	WorkspaceID    string
	GranteeKind    string
	GranteeID      string
	EffectiveVerbs []string
	LedgerRowID    string
	Content        string
}

// Runner owns the deterministic fallback path. A Runner is stateless —
// the ledger is passed per call so the same Runner instance can serve
// multiple callers + workspaces concurrently.
type Runner struct {
	ledger ledger.Store
}

// NewRunner wires the runner to a ledger store. A nil ledger is
// accepted but Run degrades to no-ledger-write (result still returned).
func NewRunner(ledgerStore ledger.Store) *Runner {
	return &Runner{ledger: ledgerStore}
}

// Request bundles the inputs Run needs to synthesise a mechanical
// result + ledger row. All fields are required; validation fails loudly
// on the zero value rather than silently producing a partially-shaped
// row.
type Request struct {
	Trigger        Trigger
	WorkspaceID    string
	ProjectID      string
	RoleID         string
	AgentID        string
	GranteeKind    string
	GranteeID      string
	Actor          string
	EffectiveVerbs []string
	Reason         string // free-form: what made the runner necessary
}

// Validate returns the first invariant violation, or nil if r is
// well-formed.
func (r Request) Validate() error {
	if _, ok := validTriggers[r.Trigger]; !ok {
		return fmt.Errorf("mechanical: invalid trigger %q", string(r.Trigger))
	}
	if r.WorkspaceID == "" {
		return errors.New("mechanical: workspace_id required")
	}
	if r.RoleID == "" {
		return errors.New("mechanical: role_id required")
	}
	if r.GranteeID == "" {
		return errors.New("mechanical: grantee_id required")
	}
	return nil
}

// Run produces a mechanical result and writes a kind:mechanical-run
// ledger row tagged provider:mechanical. Returns the Result regardless
// of ledger-write outcome; ledger errors are logged into the Result's
// Content field but do not fail the call (evidence is best-effort on
// this path, mirroring the reviewer fallback's tolerance).
func (r *Runner) Run(ctx context.Context, req Request, now time.Time) (Result, error) {
	if err := req.Validate(); err != nil {
		return Result{}, err
	}
	content := fmt.Sprintf(
		"mechanical-run: trigger=%s role=%s agent=%s grantee=%s:%s reason=%q",
		req.Trigger, req.RoleID, req.AgentID, req.GranteeKind, req.GranteeID, req.Reason,
	)
	result := Result{
		Trigger:        req.Trigger,
		RoleID:         req.RoleID,
		WorkspaceID:    req.WorkspaceID,
		GranteeKind:    req.GranteeKind,
		GranteeID:      req.GranteeID,
		EffectiveVerbs: append([]string(nil), req.EffectiveVerbs...),
		Content:        content,
	}
	if r.ledger == nil {
		return result, nil
	}
	row := ledger.LedgerEntry{
		WorkspaceID: req.WorkspaceID,
		ProjectID:   req.ProjectID,
		Type:        ledger.TypeDecision,
		Tags: []string{
			"kind:mechanical-run",
			"provider:mechanical",
			"reason:" + string(req.Trigger),
			"role_id:" + req.RoleID,
			"grantee_kind:" + req.GranteeKind,
			"grantee_id:" + req.GranteeID,
		},
		Content:    content,
		Durability: ledger.DurabilityDurable,
		SourceType: ledger.SourceSystem,
		Status:     ledger.StatusActive,
		CreatedBy:  req.Actor,
	}
	written, err := r.ledger.Append(ctx, row, now)
	if err != nil {
		// Non-fatal — preserve the Result so the caller can still
		// produce an MCP response. Include the error in Content so
		// upstream logs surface it.
		result.Content = fmt.Sprintf("%s (ledger error: %s)", content, err.Error())
		return result, nil
	}
	result.LedgerRowID = written.ID
	return result, nil
}
