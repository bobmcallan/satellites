package cli

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/processtrace"
)

func TestCompareReviewMetrics(t *testing.T) {
	prev := reviewMetrics{
		Story:     "sty_x",
		Status:    "in_progress",
		Process:   processMetrics{GatesRun: 2, Accepted: 1, Rejections: 1},
		Speed:     speedMetrics{WallClock: "10m0s"},
		Anomalies: []processtrace.Finding{{Anomaly: "status-drift", Detail: "drifted"}},
	}
	cur := reviewMetrics{
		Story:     "sty_x",
		Status:    "done",
		Process:   processMetrics{GatesRun: 3, Accepted: 3, Rejections: 0},
		Speed:     speedMetrics{WallClock: "12m0s"},
		Anomalies: []processtrace.Finding{{Anomaly: "ungated-deploy", Detail: "no gate"}},
	}

	d := compareReviewMetrics(prev, cur)

	if d.GatesRunPrev != 2 || d.GatesRunCur != 3 {
		t.Errorf("gates run delta wrong: %d → %d", d.GatesRunPrev, d.GatesRunCur)
	}
	if d.AcceptedCur != 3 || d.RejectionsCur != 0 {
		t.Errorf("accepted/rejections wrong: %+v", d)
	}
	if d.StatusPrev != "in_progress" || d.StatusCur != "done" {
		t.Errorf("status change wrong: %s → %s", d.StatusPrev, d.StatusCur)
	}
	// status-drift resolved, ungated-deploy added.
	if len(d.AnomaliesResolved) != 1 || !strings.Contains(d.AnomaliesResolved[0], "status-drift") {
		t.Errorf("resolved wrong: %v", d.AnomaliesResolved)
	}
	if len(d.AnomaliesAdded) != 1 || !strings.Contains(d.AnomaliesAdded[0], "ungated-deploy") {
		t.Errorf("added wrong: %v", d.AnomaliesAdded)
	}
}

func TestRenderDeltaMarkdown(t *testing.T) {
	d := reviewDelta{
		Story: "sty_x", GatesRunPrev: 2, GatesRunCur: 3, AcceptedPrev: 1, AcceptedCur: 3,
		RejectionsPrev: 1, RejectionsCur: 0, StatusPrev: "in_progress", StatusCur: "done",
		WallClockPrev: "10m0s", WallClockCur: "12m0s",
		AnomaliesResolved: []string{"status-drift: drifted"},
		AnomaliesAdded:    []string{},
	}
	md := renderDeltaMarkdown(d)
	for _, want := range []string{
		"## Delta vs previous",
		"Gates run: 2 → 3 (+1)",
		"Rejections: 1 → 0 (-1)",
		"Status: in_progress → done",
		"recorded, not scored",
		"Anomalies resolved:",
		"status-drift: drifted",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("delta md missing %q", want)
		}
	}
}

// TestRenderDeltaMarkdown_Unchanged covers the no-anomaly-change branch.
func TestRenderDeltaMarkdown_Unchanged(t *testing.T) {
	md := renderDeltaMarkdown(reviewDelta{Story: "s", AnomaliesAdded: []string{}, AnomaliesResolved: []string{}})
	if !strings.Contains(md, "Anomalies: unchanged") {
		t.Errorf("expected unchanged line, got %q", md)
	}
}
