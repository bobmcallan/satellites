package cli

import (
	"bytes"
	"strings"
	"testing"
)

// TestRollupVerdicts_FindingSeverity pins the finding→verdict rollup: a block
// finding yields UNRESOLVABLE (and names the artifact), an advise finding
// yields STALE, and an artifact with no finding stays OK.
func TestRollupVerdicts_FindingSeverity(t *testing.T) {
	roster := []artifactRef{
		{name: "vire-release-workflow", kind: "workflow"},
		{name: "satellites-done-review", kind: "reviewer"},
		{name: "constitution", kind: "principle"},
	}
	findings := []driftFinding{
		{Severity: "block", Code: "missing-gate", Artifact: "vire-release-workflow", Message: "names reviewer vire-ghost-review which is not materialised"},
		{Severity: "advise", Code: "nonatomic-candidate", Artifact: "satellites-done-review", Message: "carries fail-closed markers"},
	}
	got := map[string]artifactVerdict{}
	for _, v := range rollupVerdicts(roster, findings) {
		got[v.Artifact] = v
	}

	if v := got["vire-release-workflow"]; v.Verdict != "UNRESOLVABLE" || !strings.Contains(v.Reason, "vire-ghost-review") {
		t.Errorf("workflow with a dangling reviewer = %+v, want UNRESOLVABLE naming the reference", v)
	}
	if v := got["satellites-done-review"]; v.Verdict != "STALE" {
		t.Errorf("advise finding = %q, want STALE", v.Verdict)
	}
	if v := got["constitution"]; v.Verdict != "OK" {
		t.Errorf("clean artifact = %q, want OK", v.Verdict)
	}
}

// TestReportValidate_ExitOnUnresolvable proves the report returns an error
// (non-zero exit) exactly when an artifact is UNRESOLVABLE, and exits 0 when the
// worst finding is STALE or all are OK.
func TestReportValidate_ExitOnUnresolvable(t *testing.T) {
	unresolvable := []artifactVerdict{
		{Verdict: "UNRESOLVABLE", Kind: "workflow", Artifact: "w", Reason: "missing-gate: x"},
		{Verdict: "OK", Kind: "principle", Artifact: "p"},
	}
	if err := reportValidate(&bytes.Buffer{}, unresolvable, false); err == nil {
		t.Error("UNRESOLVABLE artifact must produce a non-zero exit (non-nil error)")
	}

	staleOnly := []artifactVerdict{
		{Verdict: "STALE", Kind: "reviewer", Artifact: "r", Reason: "nonatomic-candidate: x"},
		{Verdict: "OK", Kind: "skill", Artifact: "s"},
	}
	if err := reportValidate(&bytes.Buffer{}, staleOnly, false); err != nil {
		t.Errorf("STALE-only must exit 0: %v", err)
	}

	allOK := []artifactVerdict{{Verdict: "OK", Kind: "workflow", Artifact: "w"}}
	if err := reportValidate(&bytes.Buffer{}, allOK, false); err != nil {
		t.Errorf("all-OK must exit 0: %v", err)
	}
}

// TestReportValidate_OrdersWorstFirst proves UNRESOLVABLE rows sort above STALE
// above OK, so the actionable findings lead the report.
func TestReportValidate_OrdersWorstFirst(t *testing.T) {
	roster := []artifactRef{
		{name: "ok-one", kind: "skill"},
		{name: "bad-one", kind: "workflow"},
		{name: "stale-one", kind: "reviewer"},
	}
	findings := []driftFinding{
		{Severity: "block", Code: "missing-gate", Artifact: "bad-one", Message: "x"},
		{Severity: "advise", Code: "nonatomic-candidate", Artifact: "stale-one", Message: "y"},
	}
	out := rollupVerdicts(roster, findings)
	if out[0].Artifact != "bad-one" || out[len(out)-1].Artifact != "ok-one" {
		t.Errorf("rollup order = %v, want UNRESOLVABLE first / OK last", out)
	}
}
