// Reviewer-role gate.
//
// The status transition gate is structural per sty_25d5b21e: only a
// `reviewer`-role api-key can patch a story's status field or append
// the ledger. The executor key the agent holds is body-only.
//
// The gate trips only when an executor api-key is on ctx. Absence of
// an api-key role (CLI-local in-process, JWT-authed portal users)
// passes — that matches the existing CLI/membership-check pattern in
// document.go and keeps the contract narrow to api-key callers.

package verb

import (
	"context"
	"fmt"

	"github.com/bobmcallan/satellites/internal/auth"
)

func requireReviewerRole(ctx context.Context, verbName string) error {
	role := auth.APIKeyRoleFromContext(ctx)
	if role == "" || role == auth.APIKeyRoleReviewer {
		return nil
	}
	return fmt.Errorf("%s: %w: requires reviewer-role api-key (got %s)", verbName, ErrForbidden, role)
}
