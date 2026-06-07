package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/processtrace"
	"github.com/bobmcallan/satellites/internal/workstate"
)

func sampleTrace() processtrace.ProcessTrace {
	t0 := time.Date(2026, 6, 7, 1, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 6, 7, 2, 0, 0, 0, time.UTC)
	return processtrace.ProcessTrace{
		StoryID:       "sty_abc",
		StoryType:     "feature",
		WorkflowName:  "satellites-feature-workflow",
		CurrentStatus: "done",
		Transitions: []processtrace.TransitionTrace{
			{From: "backlog", To: "ready", ReviewerSkill: "satellites-story-plan-review", Status: processtrace.StatusAccepted, At: &t0, StepSummary: "plan ok"},
			{From: "ready", To: "in_progress", ReviewerSkill: "satellites-story-start-review", Status: processtrace.StatusAccepted, At: &t1},
			{From: "in_progress", To: "done", ReviewerSkill: "satellites-story-done-review", Status: processtrace.StatusRejected, RejectCount: 1, Verdict: "AC #3 has no test\nfix it"},
		},
	}
}

func TestBuildReviewMetrics(t *testing.T) {
	trace := sampleTrace()
	findings := []processtrace.Finding{{StoryID: "sty_abc", Anomaly: "ungated-deploy", Severity: "error", Detail: "deploy with no gate"}}
	evidence := []workstate.Evidence{
		{Story: "sty_abc", Kind: workstate.EvidenceCI, Label: "test", Decision: "success", Ref: "abc123"},
		{Story: "sty_abc", Kind: workstate.EvidenceGate, Label: "satellites-story-done-review", Decision: "reject"},
	}
	gen := time.Date(2026, 6, 7, 3, 0, 0, 0, time.UTC)

	m := buildReviewMetrics(trace, findings, evidence, gen)

	if m.Story != "sty_abc" || m.Type != "feature" || m.Status != "done" || m.Workflow != "satellites-feature-workflow" {
		t.Fatalf("header wrong: %+v", m)
	}
	// 3 transitions exercised: 2 accepted + 1 rejected ⇒ gates_run 3, accepted 2, rejections 1.
	if m.Process.GatesRun != 3 || m.Process.Accepted != 2 || m.Process.Rejections != 1 {
		t.Errorf("process counts wrong: run=%d accepted=%d rej=%d", m.Process.GatesRun, m.Process.Accepted, m.Process.Rejections)
	}
	if len(m.Process.RejectionReasons) != 1 || !strings.Contains(m.Process.RejectionReasons[0], "AC #3 has no test") {
		t.Errorf("rejection reason (first line only) wrong: %v", m.Process.RejectionReasons)
	}
	if strings.Contains(strings.Join(m.Process.RejectionReasons, ""), "fix it") {
		t.Errorf("rejection reason should be first line only, got %v", m.Process.RejectionReasons)
	}
	if !m.Outcome.ReachedTerminal || m.Outcome.TerminalStatus != "done" {
		t.Errorf("outcome wrong: %+v", m.Outcome)
	}
	if len(m.Outcome.CI) != 1 || m.Outcome.CI[0].Stage != "test" || m.Outcome.CI[0].Result != "success" {
		t.Errorf("CI outcome wrong (gate row must be excluded): %+v", m.Outcome.CI)
	}
	// Speed recorded from transition timestamps (t0..t1 = 1h), not scored.
	if m.Speed.WallClock != "1h0m0s" || m.Speed.FirstActivity == "" || m.Speed.LastActivity == "" {
		t.Errorf("speed wrong: %+v", m.Speed)
	}
	if len(m.Anomalies) != 1 {
		t.Errorf("anomalies not carried: %+v", m.Anomalies)
	}
}

func TestFallbackSuggestions(t *testing.T) {
	clean := buildReviewMetrics(processtrace.ProcessTrace{StoryID: "s", CurrentStatus: "done"}, nil, nil, time.Unix(0, 0).UTC())
	if !strings.Contains(fallbackSuggestions(clean), "ran clean") {
		t.Errorf("clean run should report no suggestions, got %q", fallbackSuggestions(clean))
	}
	dirty := buildReviewMetrics(sampleTrace(),
		[]processtrace.Finding{{Anomaly: "status-drift", Severity: "warn", Detail: "drifted"}},
		nil, time.Unix(0, 0).UTC())
	fb := fallbackSuggestions(dirty)
	if !strings.Contains(fb, "status-drift") || !strings.Contains(fb, "gate rejection to learn from") {
		t.Errorf("dirty fallback should surface anomaly + rejection, got %q", fb)
	}
}

func TestRenderReviewMarkdown(t *testing.T) {
	m := buildReviewMetrics(sampleTrace(), nil, nil, time.Date(2026, 6, 7, 3, 0, 0, 0, time.UTC))
	md := renderReviewMarkdown(m, "- tighten the done gate\n")
	for _, want := range []string{
		"# Session review — sty_abc",
		"## Process adherence",
		"## Outcome",
		"## Anomalies",
		"## Speed (recorded, not scored)",
		"## Improvement suggestions",
		"- tighten the done gate",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("review.md missing %q", want)
		}
	}
}
