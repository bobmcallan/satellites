package contract

import "errors"

// Status values a ContractInstance can occupy per
// docs/architecture.md §5. Terminal states (passed, failed, skipped)
// reject further transitions.
//
// pending_review is the intermediate state introduced by
// epic:v4-lifecycle-refactor (sty_b6b2de01). When the agent calls
// contract_close the CI moves claimed → pending_review and a
// kind:review task is enqueued for the embedded reviewer service to
// claim. The reviewer service writes a kind:verdict ledger row and
// flips the CI pending_review → passed | failed (sty_c6d76a5b).
const (
	StatusReady         = "ready"
	StatusClaimed       = "claimed"
	StatusPendingReview = "pending_review"
	StatusPassed        = "passed"
	StatusFailed        = "failed"
	StatusSkipped       = "skipped"
	// StatusCancelled is the terminal state a CI takes when an operator
	// or recovery agent calls contract_cancel on a claimed/pending_review
	// CI (sty_3a59a6d7). Already-terminal failed/passed CIs are NOT
	// flipped to cancelled — contract_cancel mints a successor and
	// leaves the original row untouched for audit. cancelled is treated
	// like passed/skipped by PredecessorGate's slot-relief logic when a
	// successor at the same slot exists.
	StatusCancelled = "cancelled"
)

// ErrInvalidTransition is returned when UpdateStatus is asked to move a
// ContractInstance into an unreachable target state.
var ErrInvalidTransition = errors.New("contract: invalid status transition")

// ValidTransition reports whether a CI may move from → to. Self-
// transitions are rejected (no-op writes are a caller bug). Terminal
// states refuse further transitions.
//
// Matrix:
//
//	from \ to        ready  claimed  pending_review  passed  failed  skipped
//	ready             -      ✓         -               -       -       ✓
//	claimed           -      -         ✓               ✓       ✓       ✓
//	pending_review    -      -         -               ✓       ✓       -
//	passed            -      -         -               -       -       -
//	failed            -      -         -               -       -       -
//	skipped           -      -         -               -       -       -
//
// claimed→passed and claimed→failed remain valid for the legacy inline
// reviewer path (some tests + the close path during the migration to
// uniform review-task gating still flip CIs directly). The current
// production path is pending_review → passed | failed driven by the
// embedded reviewer service (sty_c6d76a5b).
func ValidTransition(from, to string) bool {
	switch from {
	case StatusReady:
		return to == StatusClaimed || to == StatusSkipped
	case StatusClaimed:
		return to == StatusPendingReview || to == StatusPassed || to == StatusFailed || to == StatusSkipped || to == StatusCancelled
	case StatusPendingReview:
		return to == StatusPassed || to == StatusFailed || to == StatusCancelled
	case StatusPassed, StatusFailed, StatusSkipped, StatusCancelled:
		return false
	default:
		return false
	}
}

// IsKnownStatus reports whether s is one of the declared status strings.
// Used by Create to validate an explicitly-supplied initial status.
func IsKnownStatus(s string) bool {
	switch s {
	case StatusReady, StatusClaimed, StatusPendingReview, StatusPassed, StatusFailed, StatusSkipped, StatusCancelled:
		return true
	}
	return false
}
