package verb

import "testing"

func TestParseStageOutput(t *testing.T) {
	// Bare accept with summary.
	out, err := ParseStageOutput([]byte(`{"decision":"accept","notes":"ok","summary":"shipped X"}`))
	if err != nil || out.Decision != GateDecisionAccept || out.Summary != "shipped X" {
		t.Fatalf("accept = %+v err=%v", out, err)
	}

	// Reject carries notes; summary ignored.
	out, err = ParseStageOutput([]byte(`{"decision":"reject","notes":"no git record at ship stage"}`))
	if err != nil || out.Decision != GateDecisionReject || out.Notes == "" {
		t.Fatalf("reject = %+v err=%v", out, err)
	}

	// Prose-wrapped + fenced — take the last valid decision object.
	wrapped := "Reasoning about config/{a,b}/...\n```json\n{\"decision\":\"accept\",\"summary\":\"done\"}\n```\n"
	out, err = ParseStageOutput([]byte(wrapped))
	if err != nil || out.Decision != GateDecisionAccept || out.Summary != "done" {
		t.Fatalf("wrapped = %+v err=%v", out, err)
	}

	// No decision object → error, never a silent accept.
	if _, err := ParseStageOutput([]byte("no json here")); err == nil {
		t.Fatalf("expected error for missing decision object")
	}
}
