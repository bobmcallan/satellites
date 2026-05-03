package portal

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/story"
)

// storyStatusRequest is the POST body for /api/stories/{id}/status.
// Reason is optional but required for non-derivable transitions per
// AC — the handler enforces that, then plumbs the value into the
// kind:story.status_change ledger row's Content field so substrate-
// derived flips and operator flips remain distinguishable.
// sty_1d6751e9.
type storyStatusRequest struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// handleStoryStatusUpdate is the POST endpoint that powers the per-row
// + bulk operator-status-override affordances on the stories panel.
// Authorisation: caller must own the story's project. Cross-owner
// returns 404 (mirroring handleProjectDetail's leak-prevention).
// sty_1d6751e9.
func (p *Portal) handleStoryStatusUpdate(w http.ResponseWriter, r *http.Request) {
	user, ok := p.resolveUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if p.stories == nil {
		http.NotFound(w, r)
		return
	}
	storyID := r.PathValue("id")
	if storyID == "" {
		http.Error(w, "story id required", http.StatusBadRequest)
		return
	}
	var req storyStatusRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	target := strings.TrimSpace(req.Status)
	if target == "" {
		http.Error(w, "status required", http.StatusBadRequest)
		return
	}
	reason := strings.TrimSpace(req.Reason)

	_, _, memberships := p.activeWorkspace(r, user)
	existing, err := p.stories.GetByID(r.Context(), storyID, memberships)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// Cross-owner protection: if the story's project is not owned by the
	// caller, hide its existence behind a 404. Mirrors handleProjectDetail.
	if p.projects != nil && existing.ProjectID != "" {
		proj, perr := p.projects.GetByID(r.Context(), existing.ProjectID, memberships)
		if perr != nil || proj.OwnerUserID != user.ID {
			http.NotFound(w, r)
			return
		}
	}
	// Reason gate: any transition that skips intermediate states OR
	// terminates via cancelled requires an explanation. Substrate-derived
	// flips (backlog→ready, ready→in_progress, in_progress→done) leave
	// reason empty and the audit chain stays clean.
	if reasonRequired(existing.Status, target) && reason == "" {
		http.Error(w, "reason required for non-derivable transition", http.StatusBadRequest)
		return
	}

	now := time.Now().UTC()
	updated, err := p.stories.UpdateStatus(r.Context(), storyID, target, user.ID, now, memberships)
	if err != nil {
		// 422 keeps the operator's UI state intact while signalling that
		// the substrate rejected the transition (illegal jump, terminal
		// re-target, etc.).
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	// Reason audit row: stamp a kind:operator-override ledger row carrying
	// the reason text. The substrate's own kind:story.status_change row
	// (written inside UpdateStatus) records the from→to + actor; this
	// extra row carries the human "why" without polluting the substrate's
	// canonical event shape.
	if reason != "" && p.ledger != nil {
		_, _ = p.ledger.Append(r.Context(), ledger.LedgerEntry{
			WorkspaceID: existing.WorkspaceID,
			ProjectID:   existing.ProjectID,
			StoryID:     ledger.StringPtr(existing.ID),
			Type:        ledger.TypeDecision,
			Tags: []string{
				"kind:operator-override",
				"target:" + target,
				"from:" + existing.Status,
			},
			Content:    reason,
			Durability: ledger.DurabilityDurable,
			SourceType: ledger.SourceUser,
			Status:     ledger.StatusActive,
			CreatedBy:  user.ID,
		}, now)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":     true,
		"id":     updated.ID,
		"status": updated.Status,
	})
}

// reasonRequired returns true when the from→to jump bypasses the
// canonical backlog→ready→in_progress→done forward chain or terminates
// via cancelled. Same-status writes also require a reason since they're
// audit-only no-ops the operator should justify.
func reasonRequired(from, to string) bool {
	if from == to {
		return true
	}
	if to == story.StatusCancelled {
		return true
	}
	// Forward derivable jumps: each step is one slot ahead.
	switch from {
	case story.StatusBacklog:
		return to != story.StatusReady
	case story.StatusReady:
		return to != story.StatusInProgress
	case story.StatusInProgress:
		return to != story.StatusDone
	case story.StatusDone:
		return true
	case story.StatusCancelled:
		return true
	}
	return true
}
