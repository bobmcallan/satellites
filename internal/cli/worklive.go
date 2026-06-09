// Real-time engagement activity emission (epic:dynamic-workflow-status, order:1,
// sty_8e21c3ec).
//
// Engagement events are recorded locally (internal/workstate) and batch-flushed
// to the server ledger by `satellites work sync`. The server's evidence-insert
// NOTIFY trigger fires story:/project:/activity: SSE on each ledger row, so the
// portal can show a story being worked in real time — but only once the events
// reach the server. This file flushes them PROMPTLY (on engage / transition /
// edit) instead of waiting for the next batch sync, reusing the SAME idempotent
// high-water-mark flush (runWorkSync) so each event still lands exactly once.

package cli

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/cliconfig"
	"github.com/bobmcallan/satellites/internal/workstate"
)

const (
	// activityTouchWindow throttles the high-frequency START door: at most one
	// activity ping per (session, story) per window. Kept below the server's
	// activity freshness window so an actively-edited story stays "alive" in the
	// portal without a network call on every single edit.
	activityTouchWindow = 45 * time.Second
	// realtimeEmitTimeout bounds a real-time flush so it never meaningfully
	// stalls the action that triggered it; on timeout the event simply re-flushes
	// via the next `work sync` (the high-water mark keeps it exactly-once).
	realtimeEmitTimeout = 1500 * time.Millisecond
)

// realtimeEmitFn is the real-time flush, indirected through a package var so the
// engagement paths and the START door can be unit-tested without a live server.
var realtimeEmitFn = emitEngagementRealtime

// emitEngagementRealtime best-effort flushes unflushed engagement events to the
// server ledger NOW, so the story:/activity: SSE topics fire in real time rather
// than only on batch `work sync`. Idempotent (shares the flush high-water mark)
// and fail-open (any error leaves the mark unadvanced for `work sync` to catch
// up); bounded by realtimeEmitTimeout so it never blocks the caller for long.
func emitEngagementRealtime(ctx context.Context, configArg string) {
	cfg, _, _ := cliconfig.Load(configArg)
	ctx, cancel := context.WithTimeout(ctx, realtimeEmitTimeout)
	defer cancel()
	_ = runWorkSync(io.Discard, resolveStateDB(configArg), realLedgerAppender(ctx, cfg.ProjectID, configArg))
}

// touchEngagementActivity records a throttled activity touch for an engaged
// story on an allowed edit and best-effort emits it to the server, so the
// portal's activity indicator stays lit while the agent is actively editing.
// The touch re-stamps the engagement's CURRENT phase + editability under a fresh
// lease (a lease refresh that doubles as the activity ping) — it never clobbers
// the projection. Throttled to one ping per activityTouchWindow; fully
// best-effort and independent of the (already-decided) allow decision.
func touchEngagementActivity(root, configArg, session string, eng workstate.Engagement, now time.Time) {
	if strings.TrimSpace(eng.Story) == "" || strings.TrimSpace(session) == "" {
		return
	}
	store, err := workstate.Open(stateDBForRoot(root))
	if err != nil {
		return
	}
	due := store.ActivityTouchDue(session, eng.Story, now, activityTouchWindow)
	if due {
		_, _ = store.Append(workstate.Event{
			Session: session, Story: eng.Story, Phase: eng.Phase, Kind: "touch",
			LeaseUntil: now.Add(engageLeaseTTL), Editable: eng.Editable, TS: now,
		})
	}
	store.Close()
	if due {
		realtimeEmitFn(context.Background(), configArg)
	}
}
