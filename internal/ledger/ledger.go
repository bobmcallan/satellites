// Package ledger is the satellites-v4 append-only event log primitive.
// Every durable decision/evidence/trace in a project lands here as a row;
// later primitives (stories, tasks, repo scans) emit rows as their audit
// chain. Append-only at the Store interface level — no Update or Delete
// verbs exist; ledger_dereference (slice 7.2) flips Status without
// removing the row.
package ledger

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Type enum values per docs/architecture.md §6.
const (
	TypePlan          = "plan"
	TypeActionClaim   = "action_claim"
	TypeArtifact      = "artifact"
	TypeEvidence      = "evidence"
	TypeDecision      = "decision"
	TypeCloseRequest  = "close-request"
	TypeVerdict       = "verdict"
	TypeWorkflowClaim = "workflow-claim"
	TypePlanAmend     = "plan-amend"    // story_d5d88a64: dynamic plan tree
	TypeAgentCompose  = "agent-compose" // story_b19260d8: orchestrator-driven agent composition
	TypeAgentArchive  = "agent-archive" // story_b19260d8: ephemeral agent sweeper
	TypeKV            = "kv"
)

// Durability enum values per §6.
const (
	DurabilityEphemeral = "ephemeral"
	DurabilityPipeline  = "pipeline"
	DurabilityDurable   = "durable"
)

// SourceType enum values per §6.
const (
	SourceManifest  = "manifest"
	SourceFeedback  = "feedback"
	SourceAgent     = "agent"
	SourceUser      = "user"
	SourceSystem    = "system"
	SourceMigration = "migration"
)

// Status enum values per §6.
const (
	StatusActive       = "active"
	StatusArchived     = "archived"
	StatusDereferenced = "dereferenced"
)

var validTypes = map[string]struct{}{
	TypePlan: {}, TypeActionClaim: {}, TypeArtifact: {}, TypeEvidence: {},
	TypeDecision: {}, TypeCloseRequest: {}, TypeVerdict: {}, TypeWorkflowClaim: {},
	TypePlanAmend: {}, TypeAgentCompose: {}, TypeAgentArchive: {}, TypeKV: {},
}

var validDurabilities = map[string]struct{}{
	DurabilityEphemeral: {}, DurabilityPipeline: {}, DurabilityDurable: {},
}

var validSourceTypes = map[string]struct{}{
	SourceManifest: {}, SourceFeedback: {}, SourceAgent: {}, SourceUser: {},
	SourceSystem: {}, SourceMigration: {},
}

var validStatuses = map[string]struct{}{
	StatusActive: {}, StatusArchived: {}, StatusDereferenced: {},
}

// LedgerEntry is a single append-only row. No UpdatedAt — once written,
// rows do not mutate. Status flips to "dereferenced" via ledger_dereference
// (slice 7.2) — the row stays for audit. WorkspaceID cascades from the
// parent project at write time per docs/architecture.md §8.
type LedgerEntry struct {
	ID          string     `json:"id"`
	WorkspaceID string     `json:"workspace_id"`
	ProjectID   string     `json:"project_id"`
	StoryID     *string    `json:"story_id,omitempty"`
	Type        string     `json:"type"`
	Tags        []string   `json:"tags,omitempty"`
	Content     string     `json:"content"`
	Structured  []byte     `json:"structured,omitempty"`
	Durability  string     `json:"durability"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	SourceType  string     `json:"source_type"`
	Sensitive   bool       `json:"sensitive,omitempty"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	CreatedBy   string     `json:"created_by"`
	// ImpersonatingAsWorkspace is set when the row was written by a
	// global_admin acting on a workspace they are not a member of. Empty
	// for routine writes inside the actor's own workspace. Populated
	// either by the public ledger_add handler at write time or by
	// internal callers that stamp ledger.WithImpersonatingWorkspace on
	// ctx before calling Append. story_3548cde2.
	ImpersonatingAsWorkspace string `json:"impersonating_as_workspace,omitempty"`

	// BestChunkScore is populated transiently by SearchSemantic with the
	// cosine similarity of the highest-scoring chunk that backed this
	// match. Not persisted; nil on rows that didn't come through the
	// semantic path.
	BestChunkScore *float32 `json:"best_chunk_score,omitempty"`
}

// Validate returns the first invariant violation on e, or nil if e is
// well-formed. Validate covers shape only; FK existence (StoryID) is
// not enforced — the ledger is append-only and tolerates references
// to rows that may have been dereferenced.
func (e LedgerEntry) Validate() error {
	if _, ok := validTypes[e.Type]; !ok {
		return fmt.Errorf("ledger: invalid type %q", e.Type)
	}
	if _, ok := validDurabilities[e.Durability]; !ok {
		return fmt.Errorf("ledger: invalid durability %q", e.Durability)
	}
	if _, ok := validSourceTypes[e.SourceType]; !ok {
		return fmt.Errorf("ledger: invalid source_type %q", e.SourceType)
	}
	if _, ok := validStatuses[e.Status]; !ok {
		return fmt.Errorf("ledger: invalid status %q", e.Status)
	}
	switch e.Durability {
	case DurabilityEphemeral:
		if e.ExpiresAt == nil {
			return errors.New("ledger: expires_at required when durability=ephemeral")
		}
	default:
		if e.ExpiresAt != nil {
			return errors.New("ledger: expires_at allowed only when durability=ephemeral")
		}
	}
	return nil
}

// NewID returns a fresh entry id in the canonical `ldg_<8hex>` form.
func NewID() string {
	return fmt.Sprintf("ldg_%s", uuid.NewString()[:8])
}

// StringPtr returns nil for the empty string, otherwise a pointer to a
// fresh copy of s. Used by callers populating optional pointer fields
// like StoryID without sprinkling pointer construction across call sites.
func StringPtr(s string) *string {
	if s == "" {
		return nil
	}
	v := s
	return &v
}
