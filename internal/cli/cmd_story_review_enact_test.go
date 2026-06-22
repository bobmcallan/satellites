package cli

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/verb"
)

// TestEnactMismatch pins the loud-fail rule (sty_7f8f2e11, epic:enactment-
// convergence): a gate that ACCEPTED but the client enacted nothing is an error
// (named: gate + workflow + status) UNLESS the no-change is legitimate — the
// client DID enact (a v2 or v1 edge), or it was an out-of-band v2 record-only run.
// Signature: enactMismatch(decision, enacted, outOfBand, gate, workflow, status).
func TestEnactMismatch(t *testing.T) {
	acc := verb.GateDecisionAccept

	// accept + nothing enacted + not out-of-band → loud, named error
	err := enactMismatch(acc, false, false, "vire-start-review", "vire-release-workflow", "ready")
	if err == nil {
		t.Fatal("accept with no matching transition must be a loud error, not a warn")
	}
	for _, want := range []string{"vire-start-review", "vire-release-workflow", "ready"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("mismatch error must name %q; got %v", want, err)
		}
	}
	// the retired self-enact mode must not be referenced in the diagnostic.
	if strings.Contains(err.Error(), "self-enact") {
		t.Errorf("error must not reference the retired self-enact mode; got %v", err)
	}

	// reject → leaving status put is correct, no error
	if err := enactMismatch("reject", false, false, "g", "w", "ready"); err != nil {
		t.Errorf("reject should not be a mismatch error: %v", err)
	}

	// accept the client DID enact (a v2 or v1 edge) → success, no error
	if err := enactMismatch(acc, true, false, "g", "w", "ready"); err != nil {
		t.Errorf("an enacted accept should not error: %v", err)
	}

	// out-of-band v2 accept (not the lifecycle gate; verdict-only) → no error
	if err := enactMismatch(acc, false, true, "g", "w", "ready"); err != nil {
		t.Errorf("out-of-band v2 accept should not error: %v", err)
	}

	// empty workflow name → still errors, names the category-default fallback
	if err := enactMismatch(acc, false, false, "g", "", "ready"); err == nil || !strings.Contains(err.Error(), "category default") {
		t.Errorf("empty workflow should still error naming the category default: %v", err)
	}
}
