package verb

import (
	"context"
	"testing"

	"github.com/bobmcallan/satellites/internal/auth"
)

// TestReviewersAreTheOnlyStatusWriters pins the invariant that an executing
// AGENT (over MCP, or holding an executor/runner api-key) cannot move a task's
// (or story's) status with `document_upsert {status}` — the field is dropped, so
// status moves ONLY through a reviewer's status_transition ledger row. This is
// the deterministic core of "reviewers are the only status-writers" (the rule a
// task shares with a story): satellites reviews, the agent works, and the work
// path can never self-advance status.
func TestReviewersAreTheOnlyStatusWriters(t *testing.T) {
	base := context.Background()

	// An in-process / portal-session caller MAY set status (the sanctioned
	// non-agent surfaces).
	if !upsertStatusHonoured(base) {
		t.Error("a plain in-process caller should be allowed to set status")
	}

	// The agent surfaces MUST NOT: their document_upsert status is dropped, so
	// only a reviewer gate's status_transition ledger row can advance the task.
	if upsertStatusHonoured(auth.WithTransport(base, auth.TransportMCP)) {
		t.Error("an MCP caller must not be able to write status via document_upsert")
	}
	if upsertStatusHonoured(auth.WithAPIKeyRole(base, auth.APIKeyRoleExecutor)) {
		t.Error("an executor api-key must not be able to write status via document_upsert")
	}
	if upsertStatusHonoured(auth.WithAPIKeyRole(base, auth.APIKeyRoleRunner)) {
		t.Error("a runner api-key must not be able to write status via document_upsert")
	}
}
