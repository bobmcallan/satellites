// Package contract — per-AC iteration counter for plan-amend (story_d5d88a64).
//
// Every plan-amend that re-scopes a previously-amended AC bumps the
// counter for (story_id, ac_index). When the counter exceeds the cap the
// amend is rejected with ErrACIterationCap so a runaway loop ends in a
// structured failure instead of silent attempts.
//
// The counter is derived from existing CIs on the story: an AC's
// iteration count is the number of CIs in the story whose ACScope
// contains that AC index, minus one (the original CI). Deriving from
// state means there's no separate ledger to keep in sync — the cap is
// enforced by inspecting the same rows the rest of the substrate reads.
package contract

import (
	"errors"
	"os"
	"strconv"
)

// DefaultMaxACIterations is the default cap for per-AC plan amendments.
// Override via SATELLITES_MAX_AC_ITERATIONS.
const DefaultMaxACIterations = 5

// ErrACIterationCap is returned when a plan_amend would push an AC's
// iteration count above the configured cap.
var ErrACIterationCap = errors.New("contract: ac iteration cap exceeded")

// MaxACIterations returns the configured cap, falling back to
// DefaultMaxACIterations when SATELLITES_MAX_AC_ITERATIONS is unset or
// invalid.
func MaxACIterations() int {
	v := os.Getenv("SATELLITES_MAX_AC_ITERATIONS")
	if v == "" {
		return DefaultMaxACIterations
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return DefaultMaxACIterations
	}
	return n
}

// ACIterationCount counts the number of existing CIs whose ACScope
// contains acIndex. Used by plan_amend to compute the next iteration
// number for the AC.
func ACIterationCount(existing []ContractInstance, acIndex int) int {
	n := 0
	for _, ci := range existing {
		for _, idx := range ci.ACScope {
			if idx == acIndex {
				n++
				break
			}
		}
	}
	return n
}

// ValidateACScope returns ErrACIterationCap when adding amended would
// take any AC index above the cap. existing is the story's current CI
// list; amended is the slice of new CIs the plan_amend handler is about
// to create. cap is the configured per-AC iteration limit.
func ValidateACScope(existing, amended []ContractInstance, cap int) error {
	if cap <= 0 {
		cap = DefaultMaxACIterations
	}
	for _, ci := range amended {
		for _, idx := range ci.ACScope {
			next := ACIterationCount(existing, idx) + countACInBatch(amended, idx)
			if next > cap {
				return ErrACIterationCap
			}
		}
	}
	return nil
}

// countACInBatch counts how many CIs in batch (up to and including the
// caller's row) carry acIndex in their scope. Used so a single amend
// adding multiple CIs targeting the same AC also counts toward the cap.
func countACInBatch(batch []ContractInstance, acIndex int) int {
	n := 0
	for _, ci := range batch {
		for _, idx := range ci.ACScope {
			if idx == acIndex {
				n++
				break
			}
		}
	}
	return n
}
