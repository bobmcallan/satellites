package contract

import "errors"

// Status values a ContractInstance can occupy per
// docs/architecture.md §5. Terminal states (passed, failed, skipped)
// reject further transitions.
const (
	StatusReady   = "ready"
	StatusClaimed = "claimed"
	StatusPassed  = "passed"
	StatusFailed  = "failed"
	StatusSkipped = "skipped"
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
//	from \ to   ready  claimed  passed  failed  skipped
//	ready        -      ✓        -       -       ✓
//	claimed      -      -        ✓       ✓       ✓
//	passed       -      -        -       -       -
//	failed       -      -        -       -       -
//	skipped      -      -        -       -       -
func ValidTransition(from, to string) bool {
	switch from {
	case StatusReady:
		return to == StatusClaimed || to == StatusSkipped
	case StatusClaimed:
		return to == StatusPassed || to == StatusFailed || to == StatusSkipped
	case StatusPassed, StatusFailed, StatusSkipped:
		return false
	default:
		return false
	}
}

// IsKnownStatus reports whether s is one of the declared status strings.
// Used by Create to validate an explicitly-supplied initial status.
func IsKnownStatus(s string) bool {
	switch s {
	case StatusReady, StatusClaimed, StatusPassed, StatusFailed, StatusSkipped:
		return true
	}
	return false
}
