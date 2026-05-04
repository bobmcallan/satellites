// Package reviewer owns the close-time verdict path for contract
// instances whose contract document has validation_mode=llm or
// validation_mode=check-based. The package is deliberately narrow: it
// defines the Reviewer interface, the Request/Verdict types, a default
// AcceptAll reviewer, and a ChecksRunner that evaluates the
// check-based configuration without invoking an LLM.
//
// A production LLM client implements this interface in a separate
// package so tests can inject a stub verdict without depending on
// network.
package reviewer

import (
	"context"
	"time"
)

// Verdict enum values. Mirrors docs/architecture.md §5 "review-response
// protocol".
const (
	VerdictAccepted  = "accepted"
	VerdictRejected  = "rejected"
	VerdictNeedsMore = "needs_more"
)

// ValidationMode enum values read from contract document structured.
//
// ModeTask (epic:v4-lifecycle-refactor sty_b6b2de01) routes the close
// through the task queue: the close handler creates a kind:review task,
// flips the CI to pending_review, and returns. The embedded reviewer
// service subscribes to kind:review tasks, claims them, runs the
// reviewer, writes a kind:verdict ledger row tagged to the review
// task, closes the task, and on rejection spawns a successor work
// task with PriorTaskID set (sty_c6d76a5b). Replaces the inline gemini
// dispatch for contracts that opt in.
const (
	ModeAgent      = "agent"
	ModeLLM        = "llm"
	ModeCheckBased = "check-based"
	ModeTask       = "task"
)

// Request is the reviewer's input packet. The handler assembles it
// from the close-time context: contract agent_instruction, reviewer
// rubric body, the evidence markdown the agent submitted, and the
// recent ledger rows on the CI (already filtered by memberships).
//
// ACScope (story_d5d88a64) is the 1-based subset of the parent story's
// ACs the closing CI covers. When non-empty the reviewer prompt should
// limit AC-coverage checks to those indices — a develop CI scoped to
// AC=[2] doesn't need evidence for AC 1. Empty/nil = full-AC review
// (backwards-compatible default).
type Request struct {
	ContractID       string
	ContractName     string
	AgentInstruction string
	ReviewerRubric   string
	EvidenceMarkdown string
	EvidenceRefs     []string
	ACScope          []int
	RecentLedger     []LedgerSnippet
}

// HasScopedACs reports whether the Request narrows the AC-coverage
// check to a subset. Production reviewer implementations should switch
// on this — the default AcceptAll already accepts everything so the
// flag is a no-op there.
func (r Request) HasScopedACs() bool {
	return len(r.ACScope) > 0
}

// LedgerSnippet is a trimmed ledger row for reviewer context. Full
// rows are expensive; this carries only the fields the reviewer needs.
type LedgerSnippet struct {
	ID         string
	Type       string
	Tags       []string
	Content    string
	AuthoredAt time.Time
}

// UsageCost captures the reviewer's token/cost footprint so the
// handler can write a kind:llm-usage row.
type UsageCost struct {
	InputTokens  int
	OutputTokens int
	CostUSD      float64
	Model        string
}

// Verdict is the reviewer's output packet.
type Verdict struct {
	Outcome         string   // accepted | rejected | needs_more
	Rationale       string   // human-readable explanation
	PrinciplesCited []string // principle ids that informed the verdict
	ReviewQuestions []string // only on needs_more
}

// Reviewer evaluates a close-time request and returns a verdict.
// Implementations must be safe for concurrent use.
type Reviewer interface {
	Review(ctx context.Context, req Request) (Verdict, UsageCost, error)
}

// AcceptAll is the default Reviewer used when no LLM client is wired
// in. Its purpose is to keep the close path green in dev / tests; real
// deployments swap it for a production Reviewer.
type AcceptAll struct{}

// Review implements Reviewer — always returns accepted with a stub
// rationale and zero cost.
func (AcceptAll) Review(ctx context.Context, req Request) (Verdict, UsageCost, error) {
	return Verdict{Outcome: VerdictAccepted, Rationale: "accepted (default AcceptAll reviewer)"}, UsageCost{}, nil
}

// Compile-time assertion.
var _ Reviewer = AcceptAll{}
