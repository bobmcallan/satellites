package mcpserver

import (
	"context"
	"time"

	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/task"
)

// computeCIIteration returns the 1-based lap number of ci among CIs with
// the same ContractName on the same story, ordered by CreatedAt. The
// first CI of a given contract_name is iteration 1; rejection-append
// loops produce iterations 2, 3, ... Returns 1 when peers can't be
// resolved (best-effort default). sty_78ddc67b.
func (s *Server) computeCIIteration(ctx context.Context, ci contract.ContractInstance, memberships []string) int {
	if s.contracts == nil || ci.StoryID == "" || ci.ContractName == "" {
		return 1
	}
	peers, err := s.contracts.List(ctx, ci.StoryID, memberships)
	if err != nil {
		return 1
	}
	n := 0
	for _, p := range peers {
		if p.ContractName != ci.ContractName {
			continue
		}
		if p.CreatedAt.After(ci.CreatedAt) {
			continue
		}
		n++
	}
	if n == 0 {
		return 1
	}
	return n
}

// stampTaskIteration sets t.Iteration based on the parent CI's lap
// number. Pass-through when the task has no contract_instance_id or the
// CI cannot be resolved; in those cases iteration defaults to 1 via the
// store layer. sty_78ddc67b.
func (s *Server) stampTaskIteration(ctx context.Context, t task.Task, memberships []string) task.Task {
	if t.Iteration > 0 {
		return t
	}
	if t.ContractInstanceID == "" || s.contracts == nil {
		return t
	}
	ci, err := s.contracts.GetByID(ctx, t.ContractInstanceID, memberships)
	if err != nil {
		return t
	}
	t.Iteration = s.computeCIIteration(ctx, ci, memberships)
	return t
}

// _ keeps time imported when the helper grows; iteration math is t-free
// today but the surrounding handlers consume time.Time on every call.
var _ = time.Now
