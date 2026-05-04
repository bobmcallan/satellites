package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/story"
)

// handleContractCancel is the manual escape hatch for unrecoverable CI
// states (sty_3a59a6d7). It complements the auto-rejection-append loop
// in CommitReviewVerdict by handling cases the auto-loop cannot:
//
//   - terminal `failed` CIs whose successor never minted (auto-loop hit
//     the iteration cap, OR the failure happened on the legacy
//     claimed→failed path that bypasses CommitReviewVerdict);
//   - mid-flight `claimed` / `pending_review` CIs an operator wants to
//     abandon explicitly;
//   - `passed` CIs the operator wants to retire without the
//     contract_resume reopen-and-rebind dance.
//
// On success the handler:
//
//   - flips `claimed` / `pending_review` CIs to terminal `cancelled`
//     (already-terminal `failed` / `passed` rows are NOT flipped — they
//     stay in the audit chain);
//   - appends a `kind:cancellation` ledger row stamping prior_ci_id +
//     prior_status + reason;
//   - mints a successor CI at the same workflow slot via
//     mintSuccessorCI (status=ready, PriorCIID=prior.ID), tagged
//     `kind:cancellation-append`;
//   - clears the story's `blocked` status when the prior CI was the one
//     that triggered the iteration-cap escalation.
//
// AC5 of sty_3a59a6d7 forbids silent retry — this verb is operator /
// agent-invoked only; CommitReviewVerdict never calls it.
func (s *Server) handleContractCancel(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	caller, _ := UserFrom(ctx)
	ciID, err := req.RequireString("contract_instance_id")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	reason, err := req.RequireString("reason")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	memberships := s.resolveCallerMemberships(ctx, caller)
	ci, err := s.contracts.GetByID(ctx, ciID, memberships)
	if err != nil {
		body, _ := json.Marshal(map[string]any{"error": "ci_not_found"})
		return mcpgo.NewToolResultError(string(body)), nil
	}

	switch ci.Status {
	case contract.StatusClaimed, contract.StatusPendingReview,
		contract.StatusFailed, contract.StatusPassed:
		// proceed
	default:
		body, _ := json.Marshal(map[string]any{
			"error":  "ci_not_cancellable",
			"status": ci.Status,
		})
		return mcpgo.NewToolResultError(string(body)), nil
	}

	now := s.nowUTC()
	priorStatus := ci.Status

	// For non-terminal states, flip the CI to cancelled. Terminal
	// failed/passed are preserved as-is (audit invariant).
	if ci.Status == contract.StatusClaimed || ci.Status == contract.StatusPendingReview {
		if _, uerr := s.contracts.UpdateStatus(ctx, ci.ID, contract.StatusCancelled, caller.UserID, now, memberships); uerr != nil {
			return mcpgo.NewToolResultError(uerr.Error()), nil
		}
	}

	cancellationPayload, _ := json.Marshal(map[string]any{
		"prior_ci_id":   ci.ID,
		"prior_status":  priorStatus,
		"contract_name": ci.ContractName,
		"reason":        reason,
	})
	cancellationRow, lerr := s.ledger.Append(ctx, ledger.LedgerEntry{
		WorkspaceID: ci.WorkspaceID,
		ProjectID:   ci.ProjectID,
		StoryID:     ledger.StringPtr(ci.StoryID),
		ContractID:  ledger.StringPtr(ci.ID),
		Type:        ledger.TypeDecision,
		Tags:        []string{"kind:cancellation", "phase:" + ci.ContractName, "prior_status:" + priorStatus},
		Content:     fmt.Sprintf("contract_cancel on %s (prior_status=%s); reason: %s", ci.ID, priorStatus, reason),
		Structured:  cancellationPayload,
		CreatedBy:   caller.UserID,
	}, now)
	if lerr != nil {
		return mcpgo.NewToolResultError(lerr.Error()), nil
	}

	// Iteration count for the cancellation-append audit row mirrors the
	// rejection-append convention: one-based count of CIs at the same
	// (ContractName, Sequence) slot, INCLUDING the about-to-be-minted
	// successor.
	peers, _ := s.contracts.List(ctx, ci.StoryID, memberships)
	sameSlot := 0
	for _, p := range peers {
		if p.ContractName == ci.ContractName && p.Sequence == ci.Sequence {
			sameSlot++
		}
	}
	auditContent := fmt.Sprintf("appended fresh %s CI after cancellation of %s (prior_status=%s); reason: %s", ci.ContractName, ci.ID, priorStatus, reason)
	successor, appendRowID, mintErr := s.mintSuccessorCI(ctx, ci, "kind:cancellation-append", sameSlot+1, auditContent, caller.UserID, now, memberships)
	if mintErr != nil {
		return mcpgo.NewToolResultError(mintErr.Error()), nil
	}

	// If the story was blocked due to iteration-cap exhaustion on this
	// contract, clear it back to in_progress so subsequent claims pass
	// the story-status gate. story is the single writer; we only flip
	// when blocked, otherwise leave it alone.
	storyStatus := ""
	if s.stories != nil {
		st, gerr := s.stories.GetByID(ctx, ci.StoryID, memberships)
		if gerr == nil {
			storyStatus = st.Status
			if st.Status == story.StatusBlocked {
				if _, serr := s.stories.UpdateStatus(ctx, ci.StoryID, story.StatusInProgress, caller.UserID, now, memberships); serr == nil {
					storyStatus = story.StatusInProgress
				}
			}
		}
	}

	body, _ := json.Marshal(map[string]any{
		"contract_instance_id":   ci.ID,
		"prior_status":           priorStatus,
		"cancellation_ledger_id": cancellationRow.ID,
		"successor_ci_id":        successor.ID,
		"cancellation_append_id": appendRowID,
		"successor_iteration":    sameSlot + 1,
		"story_status":           storyStatus,
	})
	s.logger.Info().
		Str("method", "tools/call").
		Str("tool", "contract_cancel").
		Str("ci_id", ci.ID).
		Str("prior_status", priorStatus).
		Str("successor_ci_id", successor.ID).
		Str("story_id", ci.StoryID).
		Msg("mcp tool call")
	return mcpgo.NewToolResultText(string(body)), nil
}

// keep strconv import live for future structured-payload formatting
// without forcing every editor to re-add the import on minor edits.
var _ = strconv.Itoa
var _ time.Time
