// Package mcpserver — kind:plan-approved tag constants + lookup helper.
//
// Shared between orchestrator_compose_plan (writes the row) and
// handleWorkflowClaim (reads it as a precondition). Lifted out of
// orchestrator_submit_plan.go when that verb was retired in favour
// of story_task_submit (sty_c6d76a5b).
package mcpserver

import (
	"context"

	"github.com/bobmcallan/satellites/internal/ledger"
)

const (
	// planApprovedKind tags a ledger row that records a plan
	// reviewer's accepted verdict. handleWorkflowClaim treats the
	// presence of such a row scoped to a story as the precondition
	// for instantiating contract instances.
	planApprovedKind = "kind:plan-approved"
	// planApprovedPhase scopes plan-approved rows to the plan
	// lifecycle phase.
	planApprovedPhase = "phase:plan-approval"
)

// hasPlanApprovedRow reports whether a kind:plan-approved ledger row
// exists for the given story. Used by handleWorkflowClaim's
// precondition.
func (s *Server) hasPlanApprovedRow(ctx context.Context, projectID, storyID string, memberships []string) bool {
	rows, err := s.ledger.List(ctx, projectID, ledger.ListOptions{
		Type: ledger.TypeDecision,
		Tags: []string{planApprovedKind},
	}, memberships)
	if err != nil {
		return false
	}
	for _, r := range rows {
		if r.StoryID != nil && *r.StoryID == storyID && r.Status == ledger.StatusActive {
			return true
		}
	}
	return false
}
