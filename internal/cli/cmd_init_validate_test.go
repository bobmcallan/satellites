package cli

import (
	"bytes"
	"strings"
	"testing"
)

// TestReportInitValidate_CleanShowsOKLine proves a clean verdict set yields the
// "all governance OK" line (AC4 — back-compat plus one line).
func TestReportInitValidate_CleanShowsOKLine(t *testing.T) {
	var out bytes.Buffer
	reportInitValidate(&out, []artifactVerdict{
		{Verdict: "OK", Kind: "workflow", Artifact: "satellites-workflow"},
		{Verdict: "OK", Kind: "principle", Artifact: "constitution"},
	})
	if !strings.Contains(out.String(), "governance OK") || !strings.Contains(out.String(), "all 2") {
		t.Errorf("clean summary missing the all-OK line:\n%s", out.String())
	}
}

// TestReportInitValidate_DriftIsProminent proves a drift verdict set prints the
// non-OK artifacts (named, with reason) + the remedy (AC1).
func TestReportInitValidate_DriftIsProminent(t *testing.T) {
	var out bytes.Buffer
	reportInitValidate(&out, []artifactVerdict{
		{Verdict: "UNRESOLVABLE", Kind: "workflow", Artifact: "vire-release-workflow", Reason: "missing-gate: names vire-ghost-review"},
		{Verdict: "OK", Kind: "principle", Artifact: "constitution"},
	})
	s := out.String()
	if !strings.Contains(s, "drift") || !strings.Contains(s, "vire-release-workflow") || !strings.Contains(s, "vire-ghost-review") {
		t.Errorf("drift summary must name the unresolvable artifact + reason:\n%s", s)
	}
	if !strings.Contains(s, "satellites clear") {
		t.Errorf("drift summary must offer a remedy:\n%s", s)
	}
}

// TestRunInitValidate_NonFatalOnResolutionError proves the pass never fails init
// even when governance can't be resolved (no project configured here → the
// listing errors), returning nil (AC2).
func TestRunInitValidate_NonFatalOnResolutionError(t *testing.T) {
	var out bytes.Buffer
	// An empty config arg in this test context resolves no project/server; the
	// pass must absorb whatever validateGovernance returns and still return nil.
	if err := runInitValidate(nil, &out, "/nonexistent/satellites.toml", ""); err != nil {
		t.Errorf("init validate must be non-fatal, got: %v", err)
	}
}
