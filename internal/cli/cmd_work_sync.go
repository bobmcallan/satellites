// `satellites work sync` — flush local engagement events to the server ledger
// (sty_6e271170, epic:process-adherence). Engagement events ride the existing
// ledger_append verb (no new MCP verb) as kind engagement:<kind>, carrying
// session_id, so the server can assemble the cross-agent view and run the
// process-adherence audit. Best-effort + idempotent via a high-water mark: only
// successfully-appended events advance the mark, so a failure (offline server)
// simply re-flushes next run; the server ledger stays the authority.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workstate"
)

// engagementAppender appends one engagement event to the server ledger. Injected
// so the flush core is testable without a server.
type engagementAppender func(ev workstate.LoggedEvent) error

// runWorkSync flushes events above the store's high-water mark through append,
// advancing the mark only past events that appended successfully. On the first
// failure it stops (the rest, including the failed one, re-flush next run).
func runWorkSync(out io.Writer, stateDB string, append engagementAppender) error {
	store, err := workstate.Open(stateDB)
	if err != nil {
		return fmt.Errorf("work sync: open store: %w", err)
	}
	defer store.Close()

	last, err := store.LastFlushedSeq()
	if err != nil {
		return fmt.Errorf("work sync: read mark: %w", err)
	}
	events, err := store.EventsSince(last)
	if err != nil {
		return fmt.Errorf("work sync: read events: %w", err)
	}

	flushed, maxSeq := 0, last
	for _, ev := range events {
		if err := append(ev); err != nil {
			fmt.Fprintf(out, "warn: flush seq %d (%s) failed, will retry: %v\n", ev.Seq, ev.Kind, err)
			break
		}
		flushed++
		if ev.Seq > maxSeq {
			maxSeq = ev.Seq
		}
	}
	if maxSeq > last {
		if err := store.SetLastFlushedSeq(maxSeq); err != nil {
			return fmt.Errorf("work sync: advance mark: %w", err)
		}
	}
	fmt.Fprintf(out, "flushed %d engagement event(s) to ledger\n", flushed)
	return nil
}

// realLedgerAppender appends an engagement event via the ledger_append verb.
// The payload carries seq (the server-side dedup key — replays of the same
// (session, story, seq) are idempotent) and lease_until (the portal's lease
// view) alongside phase.
func realLedgerAppender(ctx context.Context, projectID, configPath string) engagementAppender {
	return func(ev workstate.LoggedEvent) error {
		payload, _ := json.Marshal(map[string]any{
			"phase": ev.Phase, "seq": ev.Seq, "lease_until": ev.LeaseUntil})
		req, err := json.Marshal(verb.LedgerAppendRequest{
			StoryID:   ev.Story,
			ProjectID: projectID,
			SessionID: ev.Session,
			Kind:      "engagement:" + ev.Kind,
			Body:      ev.Phase,
			Payload:   payload,
		})
		if err != nil {
			return err
		}
		_, err = dispatchVerb(ctx, "ledger_append", req, configPath, "")
		return err
	}
}
