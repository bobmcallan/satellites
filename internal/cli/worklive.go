// Prompt engagement flush (epic:dynamic-workflow-status, sty_8e21c3ec).
//
// Engagement events are recorded locally (internal/workstate) and batch-flushed
// to the server ledger by `satellites work sync`. The server's evidence-insert
// NOTIFY trigger fires the story:/project: SSE topics on each ledger row, so the
// portal's live list/detail refresh sees real status changes — but only once the
// events reach the server. This file flushes them PROMPTLY (on engage /
// transition) instead of waiting for the next batch sync, reusing the SAME
// idempotent high-water-mark flush (runWorkSync) so each event lands exactly once.

package cli

import (
	"context"
	"io"
	"time"

	"github.com/bobmcallan/satellites/internal/cliconfig"
)

// realtimeEmitTimeout bounds a real-time flush so it never meaningfully stalls
// the action that triggered it; on timeout the event simply re-flushes via the
// next `work sync` (the high-water mark keeps it exactly-once).
const realtimeEmitTimeout = 1500 * time.Millisecond

// realtimeEmitFn is the real-time flush, indirected through a package var so the
// engagement paths can be unit-tested without a live server.
var realtimeEmitFn = emitEngagementRealtime

// emitEngagementRealtime best-effort flushes unflushed engagement events to the
// server ledger NOW, so the story:/project: SSE topics fire in real time rather
// than only on batch `work sync`. Idempotent (shares the flush high-water mark)
// and fail-open (any error leaves the mark unadvanced for `work sync` to catch
// up); bounded by realtimeEmitTimeout so it never blocks the caller for long.
func emitEngagementRealtime(ctx context.Context, configArg string) {
	cfg, _, _ := cliconfig.Load(configArg)
	ctx, cancel := context.WithTimeout(ctx, realtimeEmitTimeout)
	defer cancel()
	_ = runWorkSync(io.Discard, resolveStateDB(configArg), realLedgerAppender(ctx, cfg.ProjectID, configArg))
}
