package portal

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/ledger"
)

// contractActionRequest is the POST body for the operator-override
// affordances on contract instances. Reason is optional but recorded
// on the kind:operator-override ledger row when present.
// sty_82662a66.
type contractActionRequest struct {
	Reason string `json:"reason"`
}

// handleContractComplete is the per-row "complete" affordance on the
// stories panel's contracts sub-table. The transition chain depends on
// the CI's current status:
//   - ready          → claimed → passed
//   - claimed        → passed
//   - pending_review → passed
//
// Any terminal state (passed/failed/skipped) returns 422.
// sty_82662a66.
func (p *Portal) handleContractComplete(w http.ResponseWriter, r *http.Request) {
	p.handleContractOperatorTransition(w, r, "complete")
}

// handleContractReview is the operator-approve affordance for a CI in
// pending_review. Same shape as Complete; restricted to non-terminal
// states. sty_82662a66.
func (p *Portal) handleContractReview(w http.ResponseWriter, r *http.Request) {
	p.handleContractOperatorTransition(w, r, "review")
}

func (p *Portal) handleContractOperatorTransition(w http.ResponseWriter, r *http.Request, action string) {
	user, ok := p.resolveUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if p.contracts == nil {
		http.NotFound(w, r)
		return
	}
	ciID := r.PathValue("id")
	if ciID == "" {
		http.Error(w, "contract instance id required", http.StatusBadRequest)
		return
	}
	var req contractActionRequest
	if r.Body != nil {
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req)
	}
	reason := strings.TrimSpace(req.Reason)

	_, _, memberships := p.activeWorkspace(r, user)
	ci, err := p.contracts.GetByID(r.Context(), ciID, memberships)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// Cross-owner protection: hide the CI's existence behind a 404 if
	// the project owner is not the caller. Mirrors handleProjectDetail
	// + handleStoryStatusUpdate.
	if p.projects != nil && ci.ProjectID != "" {
		proj, perr := p.projects.GetByID(r.Context(), ci.ProjectID, memberships)
		if perr != nil || proj.OwnerUserID != user.ID {
			http.NotFound(w, r)
			return
		}
	}

	fromStatus := ci.Status
	now := time.Now().UTC()

	chain, terminalErr := operatorTransitionChain(ci.Status, action)
	if terminalErr != nil {
		http.Error(w, terminalErr.Error(), http.StatusUnprocessableEntity)
		return
	}

	var updated contract.ContractInstance
	for _, target := range chain {
		updated, err = p.contracts.UpdateStatus(r.Context(), ciID, target, user.ID, now, memberships)
		if err != nil {
			if errors.Is(err, contract.ErrInvalidTransition) {
				http.Error(w, err.Error(), http.StatusUnprocessableEntity)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Audit row: kind:operator-override carries the action + transition
	// pair + optional reason. The substrate's own status emit
	// (emitStatus inside UpdateStatus) records the from→to per step;
	// this row is the human "why" that ties the chain together.
	if p.ledger != nil {
		entry := ledger.LedgerEntry{
			WorkspaceID: ci.WorkspaceID,
			ProjectID:   ci.ProjectID,
			Type:        ledger.TypeDecision,
			Tags: []string{
				"kind:operator-override",
				"action:" + action,
				"from:" + fromStatus,
				"to:" + updated.Status,
			},
			Content:    reason,
			Durability: ledger.DurabilityDurable,
			SourceType: ledger.SourceUser,
			Status:     ledger.StatusActive,
			CreatedBy:  user.ID,
		}
		if ci.StoryID != "" {
			s := ci.StoryID
			entry.StoryID = &s
		}
		cid := ci.ID
		entry.ContractID = &cid
		_, _ = p.ledger.Append(r.Context(), entry, now)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":     true,
		"id":     updated.ID,
		"status": updated.Status,
	})
}

// operatorTransitionChain returns the ordered list of target statuses a
// CI must pass through to reach passed via the operator-override
// surface. Returns an error when the CI is already terminal or the
// review action is requested against a state that has no review.
func operatorTransitionChain(from, action string) ([]string, error) {
	switch from {
	case contract.StatusReady:
		if action == "review" {
			return nil, errors.New("review unavailable: contract instance has not been claimed")
		}
		return []string{contract.StatusClaimed, contract.StatusPassed}, nil
	case contract.StatusClaimed:
		return []string{contract.StatusPassed}, nil
	case contract.StatusPendingReview:
		return []string{contract.StatusPassed}, nil
	}
	return nil, errors.New("contract instance is in a terminal state")
}
