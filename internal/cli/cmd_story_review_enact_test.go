package cli

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/verb"
)

// TestEnactMismatch pins the loud-fail rule (sty_7f8f2e11): a gate that ACCEPTED
// but enacted nothing is an error (named: gate + workflow + status) UNLESS the
// no-change is the legitimate out-of-band v2 record-only case.
func TestEnactMismatch(t *testing.T) {
	acc := verb.GateDecisionAccept

	// accept + no matching transition (not v2) → loud, named error
	err := enactMismatch(acc, false, false, "vire-start-review", "vire-release-workflow", "ready")
	if err == nil {
		t.Fatal("accept with no matching transition must be a loud error, not a warn")
	}
	for _, want := range []string{"vire-start-review", "vire-release-workflow", "ready"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("mismatch error must name %q; got %v", want, err)
		}
	}

	// reject → leaving status put is correct, no error
	if err := enactMismatch("reject", false, false, "g", "w", "ready"); err != nil {
		t.Errorf("reject should not be a mismatch error: %v", err)
	}

	// out-of-band v2 (isV2 && !enactV2) → record-only, expected no-change
	if err := enactMismatch(acc, true, false, "g", "w", "ready"); err != nil {
		t.Errorf("out-of-band v2 accept should not error: %v", err)
	}

	// v2 edge that SHOULD enact but didn't → still a mismatch (error)
	if err := enactMismatch(acc, true, true, "g", "w", "ready"); err == nil {
		t.Error("a governing v2 accept that enacted nothing should error")
	}

	// empty workflow name → still errors, names the category-default fallback
	if err := enactMismatch(acc, false, false, "g", "", "ready"); err == nil || !strings.Contains(err.Error(), "category default") {
		t.Errorf("empty workflow should still error naming the category default: %v", err)
	}
}
