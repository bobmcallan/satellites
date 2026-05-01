// Package contract is the satellites-v4 contract_instance primitive: the
// ordered list of CI rows for a story IS the workflow per docs/
// architecture.md §5 ("Workflow is a list of contract names per story").
// The contract definition itself lives as a `document{type=contract}` row
// from story_509f1111; this package only adds the CI table + FK.
//
// No Delete verb: CIs persist for audit per principle pr_0c11b762
// ("Evidence is the primary trust leverage").
package contract

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ContractInstance is one slot in a story's workflow. Fields match
// docs/architecture.md §5 verbatim. WorkspaceID + ProjectID cascade from
// the parent story at Create time per principle pr_0779e5af.
//
// ClaimedViaGrantID binds the CI to the role-grant the caller held at
// claim time. The claim handler resolves the caller's orchestrator
// grant before calling Claim, so this field is authoritative for both
// the auth check (enforce hook) and the amend path (same-grant
// re-claim).
type ContractInstance struct {
	ID                 string    `json:"id"`
	WorkspaceID        string    `json:"workspace_id"`
	ProjectID          string    `json:"project_id"`
	StoryID            string    `json:"story_id"`
	ContractID         string    `json:"contract_id"`
	ContractName       string    `json:"contract_name"`
	Sequence           int       `json:"sequence"`
	Status             string    `json:"status"`
	ClaimedViaGrantID  string    `json:"claimed_via_grant_id,omitempty"`
	ClaimedAt          time.Time `json:"claimed_at,omitempty"`
	PlanLedgerID       string    `json:"plan_ledger_id,omitempty"`
	CloseLedgerID      string    `json:"close_ledger_id,omitempty"`
	RequiredForClose   bool      `json:"required_for_close"`
	ACScope            []int     `json:"ac_scope,omitempty"`
	ParentInvocationID string    `json:"parent_invocation_id,omitempty"`
	// AgentID names the type=agent document allocated to this CI
	// (story_b39b393f). When non-empty, the claim handler reads the
	// agent's permission_patterns and writes them into the
	// action_claim ledger row instead of trusting the caller-submitted
	// patterns. Empty in the legacy claim path; populated once the
	// orchestrator role (story_488b8223) lands.
	AgentID string `json:"agent_id,omitempty"`
	// PriorCIID is set when this CI was appended in response to a
	// review-rejection on a prior CI of the same contract type
	// (epic:v4-lifecycle-refactor sty_bbe732af). The new CI inherits
	// the contract document, required role, and rubric; the rejection
	// reason lives on the ledger and is reachable via standard reads
	// from the new CI. Empty for the first attempt.
	PriorCIID string    `json:"prior_ci_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// NewID returns a fresh contract_instance id in the canonical
// `ci_<8hex>` form.
func NewID() string {
	return fmt.Sprintf("ci_%s", uuid.NewString()[:8])
}
