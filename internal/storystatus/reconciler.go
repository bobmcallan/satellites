// Package storystatus implements the derived-status reconciler for
// stories (sty_e805a01a, epic:status-bus-v1).
//
// The reconciler subscribes to ledger.append via the workspace-agnostic
// ledger.Listener bus interface. On every appended row that carries a
// story_id and a CI-state-transition kind tag (action_claim, close-
// request, verdict, contract-skip, contract-fail), it loads the story's
// CI rows, computes derived status from the matrix below, and writes
// back via stories.UpdateStatus when the value differs.
//
// Derivation rule (evaluated in order):
//
//	any CI in failed                 → in_progress
//	any CI in claimed/pending_review → in_progress
//	every required CI passed/skipped → done
//	no CI ever advanced past ready   → backlog
//	otherwise                        → ready
//
// Until sty_dc121948 lands, stories.UpdateStatus enforces a forward-
// only walk (backlog→ready→in_progress→done). Derivation-driven jumps
// that violate this guard are caught and rate-limited at WARN; the
// row stays at its prior status. sty_dc121948 will add a reason
// parameter and relax the table.
package storystatus

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/ternarybob/arbor"

	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/story"
)

// derivedActor is recorded as the actor on stories.UpdateStatus calls
// originating from the reconciler. The audit trail explicitly carries
// "derived" rather than masquerading as a user.
const derivedActor = "system:reconciler"

// Reconciler is the singleton ledger-bus subscriber that recomputes
// derived story status. Construct via New, install via ledger.Store
// AddListener at boot, and (optionally) call Backfill once after
// wiring to repair drift.
type Reconciler struct {
	stories   story.Store
	contracts contract.Store
	logger    arbor.ILogger

	rejMu sync.Mutex
	// rejected tracks the (story_id, from→to) tuples that have already
	// emitted a transition_rejected WARN in this process. Keys
	// suppress duplicate noise until sty_dc121948 lands.
	rejected map[string]struct{}
}

// New constructs a Reconciler. Logger may be nil — silently dropped.
func New(stories story.Store, contracts contract.Store, logger arbor.ILogger) *Reconciler {
	return &Reconciler{
		stories:   stories,
		contracts: contracts,
		logger:    logger,
		rejected:  make(map[string]struct{}),
	}
}

// OnAppend implements ledger.Listener. It filters for CI-state-
// transition events that carry a story_id, then defers to Reconcile.
// Errors are logged inside Reconcile; OnAppend returns nothing so a
// downstream subscriber's failure cannot abort the writer.
func (r *Reconciler) OnAppend(ctx context.Context, entry ledger.LedgerEntry) {
	if entry.StoryID == nil || *entry.StoryID == "" {
		return
	}
	if !triggerKind(entry.Tags) {
		return
	}
	_ = r.Reconcile(ctx, *entry.StoryID)
}

// triggerKind reports whether tags carry a CI-state-transition kind
// the reconciler must respond to. The set is intentionally narrow:
// kind:plan / kind:evidence / kind:role-grant / kind:task-* are bus
// noise from this story's perspective and are dropped here.
func triggerKind(tags []string) bool {
	for _, t := range tags {
		switch t {
		case "kind:action_claim",
			"kind:close-request",
			"kind:verdict",
			"kind:contract-skip",
			"kind:contract-fail":
			return true
		}
	}
	return false
}

// Reconcile loads the story's CI rows, computes derived status, and
// writes via stories.UpdateStatus when the value differs. Pure on
// (storyID + CI store contents) — safe to re-run on every event;
// callers do not need to dedupe.
func (r *Reconciler) Reconcile(ctx context.Context, storyID string) error {
	if storyID == "" {
		return fmt.Errorf("storystatus: story_id required")
	}
	st, err := r.stories.GetByID(ctx, storyID, nil)
	if err != nil {
		// Story may be cancelled / archived between event and reconcile;
		// not an error worth shouting about.
		return nil
	}
	cis, err := r.contracts.List(ctx, storyID, nil)
	if err != nil {
		if r.logger != nil {
			r.logger.Warn().Str("story_id", storyID).Str("error", err.Error()).Msg("storystatus: contract list failed")
		}
		return nil
	}
	derived := derive(cis)
	if derived == "" || derived == st.Status {
		return nil
	}
	if _, err := r.stories.UpdateStatus(ctx, storyID, derived, derivedActor, st.UpdatedAt, nil); err != nil {
		r.notRejected(storyID, st.Status, derived, err)
	}
	return nil
}

// notRejected emits a one-shot WARN per (story_id, from→to) tuple so
// the log stays signal-rich while sty_dc121948 is in flight.
func (r *Reconciler) notRejected(storyID, from, to string, err error) {
	key := storyID + "|" + from + "|" + to
	r.rejMu.Lock()
	_, seen := r.rejected[key]
	if !seen {
		r.rejected[key] = struct{}{}
	}
	r.rejMu.Unlock()
	if seen || r.logger == nil {
		return
	}
	r.logger.Warn().
		Str("story_id", storyID).
		Str("from", from).
		Str("to", to).
		Str("reason", "derived").
		Str("error", err.Error()).
		Msg("storystatus: transition_rejected (sty_dc121948 will relax this guard)")
}

// derive computes the derived status from a slice of CIs. Empty CI
// slice returns the empty string — caller treats this as "leave the
// story alone" (a story with no plan composed yet keeps its current
// status; nothing to derive from).
func derive(cis []contract.ContractInstance) string {
	if len(cis) == 0 {
		return ""
	}
	// Pre-sort by sequence so the rule evaluation is deterministic in
	// the rare case where two CIs share a status (no behavioural
	// effect — purely test-friendliness).
	sort.SliceStable(cis, func(i, j int) bool { return cis[i].Sequence < cis[j].Sequence })

	allTerminal := true
	anyAdvanced := false
	for _, ci := range cis {
		switch ci.Status {
		case contract.StatusFailed:
			return story.StatusInProgress
		case contract.StatusClaimed, contract.StatusPendingReview:
			return story.StatusInProgress
		}
		if ci.Status != contract.StatusReady {
			anyAdvanced = true
		}
		if ci.Status != contract.StatusPassed && ci.Status != contract.StatusSkipped {
			// Only required_for_close CIs are gated for the done check.
			// Optional CIs that are still ready do not block done.
			if ci.RequiredForClose {
				allTerminal = false
			}
		}
	}
	if allTerminal {
		return story.StatusDone
	}
	if !anyAdvanced {
		return story.StatusBacklog
	}
	return story.StatusReady
}

// Backfill walks stories and runs Reconcile on each. Returns the
// counts of stories whose status was advanced (touched) vs. whose
// reconcile attempt errored (errored). Caller-supplied story slice
// keeps the package decoupled from project.Store; main.go enumerates
// projects and aggregates stories before calling.
func (r *Reconciler) Backfill(ctx context.Context, stories []story.Story) (touched, errored int) {
	for _, st := range stories {
		// Skip terminal stories — they shouldn't be moved by derivation.
		if st.Status == story.StatusDone || st.Status == story.StatusCancelled {
			continue
		}
		prior := st.Status
		if err := r.Reconcile(ctx, st.ID); err != nil {
			errored++
			continue
		}
		// Re-read; if the status changed the reconciler advanced it.
		updated, err := r.stories.GetByID(ctx, st.ID, nil)
		if err != nil {
			errored++
			continue
		}
		if updated.Status != prior {
			touched++
		}
	}
	return touched, errored
}

// kindOf is a tiny convenience for tests asserting which kind tag a
// caller-built ledger entry carries. Returns the first kind: tag
// suffix or "" when none present.
func kindOf(tags []string) string {
	for _, t := range tags {
		if strings.HasPrefix(t, "kind:") {
			return strings.TrimPrefix(t, "kind:")
		}
	}
	return ""
}
