// Review delta (sty_37fef613, epic:measure-mode M6). The re-run loop itself is
// already the existing `satellites story status_transition --skill <gate>
// <story>` primitive; M6 adds only the COMPARISON between two successive
// `evidence review` runs. compareReviewMetrics + renderDeltaMarkdown are pure
// (no I/O) so they unit-test directly; the wiring lives in evidence review's
// --compare flag.

package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bobmcallan/satellites/internal/processtrace"
)

// reviewDelta is the change between a previous and a current review's metrics —
// the machine-readable delta.json.
type reviewDelta struct {
	Story             string   `json:"story"`
	GatesRunPrev      int      `json:"gates_run_prev"`
	GatesRunCur       int      `json:"gates_run_cur"`
	AcceptedPrev      int      `json:"accepted_prev"`
	AcceptedCur       int      `json:"accepted_cur"`
	RejectionsPrev    int      `json:"rejections_prev"`
	RejectionsCur     int      `json:"rejections_cur"`
	StatusPrev        string   `json:"status_prev"`
	StatusCur         string   `json:"status_cur"`
	WallClockPrev     string   `json:"wall_clock_prev,omitempty"`
	WallClockCur      string   `json:"wall_clock_cur,omitempty"`
	AnomaliesAdded    []string `json:"anomalies_added"`
	AnomaliesResolved []string `json:"anomalies_resolved"`
}

// compareReviewMetrics folds two review metrics into their delta. Pure.
func compareReviewMetrics(prev, cur reviewMetrics) reviewDelta {
	d := reviewDelta{
		Story:             cur.Story,
		GatesRunPrev:      prev.Process.GatesRun,
		GatesRunCur:       cur.Process.GatesRun,
		AcceptedPrev:      prev.Process.Accepted,
		AcceptedCur:       cur.Process.Accepted,
		RejectionsPrev:    prev.Process.Rejections,
		RejectionsCur:     cur.Process.Rejections,
		StatusPrev:        prev.Status,
		StatusCur:         cur.Status,
		WallClockPrev:     prev.Speed.WallClock,
		WallClockCur:      cur.Speed.WallClock,
		AnomaliesAdded:    []string{},
		AnomaliesResolved: []string{},
	}
	prevSet := anomalyKeySet(prev.Anomalies)
	curSet := anomalyKeySet(cur.Anomalies)
	for k := range curSet {
		if !prevSet[k] {
			d.AnomaliesAdded = append(d.AnomaliesAdded, k)
		}
	}
	for k := range prevSet {
		if !curSet[k] {
			d.AnomaliesResolved = append(d.AnomaliesResolved, k)
		}
	}
	sort.Strings(d.AnomaliesAdded)
	sort.Strings(d.AnomaliesResolved)
	return d
}

// anomalyKeySet keys findings by code + detail so the same anomaly across two
// runs matches and a resolved/added one is detectable.
func anomalyKeySet(fs []processtrace.Finding) map[string]bool {
	set := make(map[string]bool, len(fs))
	for _, f := range fs {
		set[anomalyKey(f)] = true
	}
	return set
}

func anomalyKey(f processtrace.Finding) string {
	return f.Anomaly + ": " + f.Detail
}

// renderDeltaMarkdown renders the "## Delta vs previous" section appended to
// review.md. Pure.
func renderDeltaMarkdown(d reviewDelta) string {
	var b strings.Builder
	b.WriteString("\n## Delta vs previous\n\n")
	fmt.Fprintf(&b, "- Gates run: %d → %d (%+d)\n", d.GatesRunPrev, d.GatesRunCur, d.GatesRunCur-d.GatesRunPrev)
	fmt.Fprintf(&b, "- Accepted: %d → %d (%+d)\n", d.AcceptedPrev, d.AcceptedCur, d.AcceptedCur-d.AcceptedPrev)
	fmt.Fprintf(&b, "- Rejections: %d → %d (%+d)\n", d.RejectionsPrev, d.RejectionsCur, d.RejectionsCur-d.RejectionsPrev)
	fmt.Fprintf(&b, "- Status: %s → %s\n", dash(d.StatusPrev), dash(d.StatusCur))
	fmt.Fprintf(&b, "- Wall-clock: %s → %s (recorded, not scored)\n", dash(d.WallClockPrev), dash(d.WallClockCur))
	if len(d.AnomaliesResolved) > 0 {
		b.WriteString("- Anomalies resolved:\n")
		for _, a := range d.AnomaliesResolved {
			fmt.Fprintf(&b, "  - %s\n", a)
		}
	}
	if len(d.AnomaliesAdded) > 0 {
		b.WriteString("- Anomalies added:\n")
		for _, a := range d.AnomaliesAdded {
			fmt.Fprintf(&b, "  - %s\n", a)
		}
	}
	if len(d.AnomaliesResolved) == 0 && len(d.AnomaliesAdded) == 0 {
		b.WriteString("- Anomalies: unchanged\n")
	}
	return b.String()
}
