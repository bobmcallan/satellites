package reviewer

import (
	"context"
	"testing"
)

func TestAcceptAll_Accepted(t *testing.T) {
	t.Parallel()
	r := AcceptAll{}
	v, _, err := r.Review(context.Background(), Request{})
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if v.Outcome != VerdictAccepted {
		t.Fatalf("outcome: %q", v.Outcome)
	}
}

func TestRunChecks_ArtifactExists_Pass(t *testing.T) {
	t.Parallel()
	checks := []Check{
		{Name: "plan_present", Type: "artifact_exists", Config: map[string]string{"artifact": "plan.md"}},
	}
	v, outcomes := RunChecks(checks, ChecksInput{Artifacts: map[string]bool{"plan.md": true}})
	if v.Outcome != VerdictAccepted {
		t.Fatalf("outcome: %q", v.Outcome)
	}
	if len(outcomes) != 1 || !outcomes[0].Passed {
		t.Fatalf("outcomes: %+v", outcomes)
	}
}

func TestRunChecks_ArtifactExists_Fail(t *testing.T) {
	t.Parallel()
	checks := []Check{
		{Name: "plan_present", Type: "artifact_exists", Config: map[string]string{"artifact": "plan.md"}},
	}
	v, outcomes := RunChecks(checks, ChecksInput{Artifacts: map[string]bool{}})
	if v.Outcome != VerdictRejected {
		t.Fatalf("outcome: %q", v.Outcome)
	}
	if outcomes[0].Passed {
		t.Fatalf("expected fail, got %+v", outcomes[0])
	}
	if outcomes[0].Message == "" {
		t.Fatalf("expected failure message")
	}
}

func TestRunChecks_EmptyChecks(t *testing.T) {
	t.Parallel()
	v, _ := RunChecks(nil, ChecksInput{})
	if v.Outcome != VerdictAccepted {
		t.Fatalf("no checks should accept, got %q", v.Outcome)
	}
}

func TestRunChecks_UnknownTypePasses(t *testing.T) {
	t.Parallel()
	checks := []Check{{Name: "unknown", Type: "bogus", Config: map[string]string{}}}
	v, outcomes := RunChecks(checks, ChecksInput{})
	if v.Outcome != VerdictAccepted {
		t.Fatalf("unknown check should pass, got %q", v.Outcome)
	}
	if !outcomes[0].Passed {
		t.Fatalf("expected unknown=passed")
	}
}
