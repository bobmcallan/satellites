package reviewer

import (
	"fmt"
	"strings"
)

// Check is a single server-side check declaration on a contract
// document. Mirrors the shape used by existing v3 contracts (see
// pprod contract docs): `{name, type, config{...}}`.
type Check struct {
	Name   string            `json:"name"`
	Type   string            `json:"type"`
	Config map[string]string `json:"config"`
}

// CheckOutcome is the per-check result.
type CheckOutcome struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Passed  bool   `json:"passed"`
	Message string `json:"message,omitempty"`
}

// ChecksInput is the data the runner needs to evaluate the known
// check types. It is populated by the handler from the CI's ledger
// rows.
type ChecksInput struct {
	// Artifacts carries the set of artifact names present in the CI's
	// ledger (e.g. from the phase:plan artifact rows). Populated from
	// rows with tag "artifact:<name>".
	Artifacts map[string]bool
}

// RunChecks evaluates each check against input. Returns the per-check
// outcomes plus an aggregate verdict: all passed → accepted; any
// failed → rejected.
func RunChecks(checks []Check, input ChecksInput) (Verdict, []CheckOutcome) {
	outcomes := make([]CheckOutcome, 0, len(checks))
	allPassed := true
	for _, c := range checks {
		outcome := evaluate(c, input)
		outcomes = append(outcomes, outcome)
		if !outcome.Passed {
			allPassed = false
		}
	}
	v := Verdict{Rationale: summarise(outcomes)}
	if allPassed {
		v.Outcome = VerdictAccepted
	} else {
		v.Outcome = VerdictRejected
	}
	return v, outcomes
}

// evaluate dispatches on check.Type. Unknown types are treated as
// passing — the reviewer rubric catches logic the runner doesn't
// understand.
func evaluate(c Check, input ChecksInput) CheckOutcome {
	switch c.Type {
	case "artifact_exists":
		artifact := c.Config["artifact"]
		if artifact == "" {
			return CheckOutcome{Name: c.Name, Type: c.Type, Passed: false, Message: "artifact_exists requires config.artifact"}
		}
		if input.Artifacts[artifact] {
			return CheckOutcome{Name: c.Name, Type: c.Type, Passed: true}
		}
		return CheckOutcome{Name: c.Name, Type: c.Type, Passed: false, Message: fmt.Sprintf("artifact %q not found in CI ledger", artifact)}
	default:
		return CheckOutcome{Name: c.Name, Type: c.Type, Passed: true, Message: "unknown check type; skipped"}
	}
}

func summarise(outcomes []CheckOutcome) string {
	if len(outcomes) == 0 {
		return "no checks declared"
	}
	parts := make([]string, 0, len(outcomes))
	for _, o := range outcomes {
		status := "pass"
		if !o.Passed {
			status = "FAIL"
		}
		parts = append(parts, o.Name+"="+status)
	}
	return strings.Join(parts, "; ")
}
