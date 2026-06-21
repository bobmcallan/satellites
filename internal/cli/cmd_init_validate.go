// The post-scaffold governance validate pass `satellites init` runs
// (epic:governance-lifecycle order-3). The common operator flow is `satellites
// update` OUT of session, then `init`/bootstrap — and nothing used to
// re-validate existing governance against the new binary, so drift (a workflow
// naming a reviewer the new binary can't resolve) stayed invisible until a gate
// broke. init now runs the SAME static validate computation the `validate`
// command uses (`validateGovernance` — no duplicate logic) and surfaces any
// drift. It is NON-FATAL: init always completes; the report is advisory.

package cli

import (
	"context"
	"fmt"
	"io"
)

// runInitValidate runs the static governance validate pass and prints a concise
// summary. Always returns nil — a resolution error (no project / offline) is a
// note, never a failure: init must complete regardless (AC2).
func runInitValidate(ctx context.Context, out io.Writer, configArg, userArg string) error {
	verdicts, err := validateGovernance(ctx, configArg, userArg)
	if err != nil {
		fmt.Fprintf(out, "  = governance validate skipped (%v) — run `satellites validate` once configured\n", err)
		return nil
	}
	reportInitValidate(out, verdicts)
	return nil
}

// reportInitValidate renders the concise init summary: an "all governance OK"
// line when clean, else the non-OK artifacts + remedy. Pure renderer so the
// non-fatal, drift, and clean shapes are unit-testable without a substrate.
func reportInitValidate(out io.Writer, verdicts []artifactVerdict) {
	var bad []artifactVerdict
	for _, v := range verdicts {
		if v.Verdict != "OK" {
			bad = append(bad, v)
		}
	}
	if len(bad) == 0 {
		fmt.Fprintf(out, "  = governance OK — all %d artifact(s) resolve against this binary\n", len(verdicts))
		return
	}
	fmt.Fprintf(out, "  ! governance drift — %d artifact(s) need attention (run `satellites validate` for detail):\n", len(bad))
	for _, v := range bad {
		fmt.Fprintf(out, "      • %-13s %s — %s\n", v.Verdict, v.Artifact, v.Reason)
	}
	fmt.Fprintln(out, "      remedy: re-author/re-upload the artifact, or archive it with `satellites clear`.")
}
