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
const (
	ModeAgent      = "agent"
	ModeLLM        = "llm"
	ModeCheckBased = "check-based"
)

// Request is the reviewer's input packet. The handler assembles it
// from the close-time context: contract agent_instruction, reviewer
// rubric body, the evidence markdown the agent submitted, and the
// recent ledger rows on the CI (already filtered by memberships).
type Request struct {
	ContractID       string
	ContractName     string
	AgentInstruction string
	ReviewerRubric   string
	EvidenceMarkdown string
	EvidenceRefs     []string
	RecentLedger     []LedgerSnippet
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
