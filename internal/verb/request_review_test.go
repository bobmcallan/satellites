package verb

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/workflow"
)

func TestPickTransition_DeclarativeSingleGated(t *testing.T) {
	wf := mustParseWF(t, `---
name: feat
applies_to: [feature]
---

`+"```yaml"+`
states: [planning, planned]
transitions:
  - {from: planning, to: planned, reviewer_skill: "plan-review"}
`+"```"+`
`)
	tr, skill, dynamic, err := pickTransition(wf, "planning")
	if err != nil {
		t.Fatalf("pickTransition: %v", err)
	}
	if dynamic {
		t.Fatalf("dynamic = true, want false")
	}
	if skill != "plan-review" {
		t.Fatalf("skill = %q, want plan-review", skill)
	}
	if tr.To != "planned" {
		t.Fatalf("transition.To = %q, want planned", tr.To)
	}
}

func TestPickTransition_DynamicWinsOverGated(t *testing.T) {
	wf := mustParseWF(t, `---
name: dyn
applies_to: [feature]
---

`+"```yaml"+`
states: [triage, simple, complex]
transitions:
  - {from: triage, to: simple,  reviewer_skill: "g", dynamic: true}
  - {from: triage, to: complex, reviewer_skill: "g", dynamic: true}
`+"```"+`
`)
	_, skill, dynamic, err := pickTransition(wf, "triage")
	if err != nil {
		t.Fatalf("pickTransition: %v", err)
	}
	if !dynamic {
		t.Fatalf("expected dynamic=true")
	}
	if skill != "dyn" {
		t.Fatalf("dynamic gate skill = %q, want workflow name 'dyn'", skill)
	}
}

func TestPickTransition_NoOutgoing(t *testing.T) {
	wf := mustParseWF(t, `---
name: feat
applies_to: [feature]
---

`+"```yaml"+`
states: [planning, planned]
transitions:
  - {from: planning, to: planned, reviewer_skill: "plan-review"}
`+"```"+`
`)
	_, _, _, err := pickTransition(wf, "planned")
	if err == nil || !strings.Contains(err.Error(), "no outgoing transitions") {
		t.Fatalf("expected no-outgoing error, got %v", err)
	}
}

func TestPickTransition_MultipleGatedMustBeDynamic(t *testing.T) {
	wf := mustParseWF(t, `---
name: feat
applies_to: [feature]
---

`+"```yaml"+`
states: [a, b, c]
transitions:
  - {from: a, to: b, reviewer_skill: "skill1"}
  - {from: a, to: c, reviewer_skill: "skill2"}
`+"```"+`
`)
	_, _, _, err := pickTransition(wf, "a")
	if err == nil || !strings.Contains(err.Error(), "multiple gated") {
		t.Fatalf("expected multiple-gated error, got %v", err)
	}
}

func TestPickTransition_NoGatedTransition(t *testing.T) {
	wf := mustParseWF(t, `---
name: feat
applies_to: [feature]
---

`+"```yaml"+`
states: [a, b]
transitions:
  - {from: a, to: b, reviewer_skill: ""}
`+"```"+`
`)
	_, _, _, err := pickTransition(wf, "a")
	if err == nil || !strings.Contains(err.Error(), "no gated transition") {
		t.Fatalf("expected no-gated error, got %v", err)
	}
}

func TestParseGateOutput_AcceptPlain(t *testing.T) {
	out, err := parseGateOutput([]byte(`{"decision":"accept","notes":"ok"}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if out.Decision != GateDecisionAccept || out.Notes != "ok" {
		t.Fatalf("out = %+v", out)
	}
}

func TestParseGateOutput_WrappedFence(t *testing.T) {
	out, err := parseGateOutput([]byte("```json\n{\"decision\":\"reject\",\"notes\":\"nope\"}\n```\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if out.Decision != GateDecisionReject || out.Notes != "nope" {
		t.Fatalf("out = %+v", out)
	}
}

func TestParseGateOutput_PreambleAndJSON(t *testing.T) {
	out, err := parseGateOutput([]byte("Some preamble.\n\n{\"decision\":\"accept\",\"next_status\":\"complex\"}"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if out.Decision != GateDecisionAccept {
		t.Fatalf("decision = %q", out.Decision)
	}
	if out.NextStatus != "complex" {
		t.Fatalf("next_status = %q, want complex", out.NextStatus)
	}
}

func TestParseGateOutput_InvalidDecision(t *testing.T) {
	_, err := parseGateOutput([]byte(`{"decision":"maybe"}`))
	if err == nil || !strings.Contains(err.Error(), "invalid decision") {
		t.Fatalf("expected invalid-decision error, got %v", err)
	}
}

func TestParseGateOutput_Malformed(t *testing.T) {
	_, err := parseGateOutput([]byte(`not json at all`))
	if err == nil || !strings.Contains(err.Error(), "parse output") {
		t.Fatalf("expected parse error, got %v", err)
	}
}

func TestDefaultSkillReader_RejectsAbsolutePath(t *testing.T) {
	_, err := defaultSkillReader("/tmp", "/etc/passwd")
	if err == nil || !strings.Contains(err.Error(), "must be relative") {
		t.Fatalf("expected absolute-path rejection, got %v", err)
	}
}

func TestDefaultSkillReader_RejectsParentEscape(t *testing.T) {
	_, err := defaultSkillReader("/tmp", "../../etc/passwd")
	if err == nil || !strings.Contains(err.Error(), "must be relative") {
		t.Fatalf("expected parent-escape rejection, got %v", err)
	}
}

func mustParseWF(t *testing.T, raw string) *workflow.Workflow {
	t.Helper()
	wf, err := workflow.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("parse workflow: %v", err)
	}
	return wf
}
