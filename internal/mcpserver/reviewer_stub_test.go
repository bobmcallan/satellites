package mcpserver

import (
	"context"

	"github.com/bobmcallan/satellites/internal/reviewer"
)

// stubReviewer is the in-process reviewer double used by orchestrator
// plan-approval tests (and previously by the inline-reviewer tests
// retired in epic:v4-lifecycle-refactor sty_e20e1537). Returns a
// fixed verdict + usage so tests can assert on the close path's
// reviewer-driven branches without depending on gemini.
type stubReviewer struct {
	verdict reviewer.Verdict
	usage   reviewer.UsageCost
	calls   int
}

// Review implements reviewer.Reviewer.
func (s *stubReviewer) Review(_ context.Context, _ reviewer.Request) (reviewer.Verdict, reviewer.UsageCost, error) {
	s.calls++
	return s.verdict, s.usage, nil
}
