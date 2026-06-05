// SSE trigger bus endpoint (sty_b6e39eb8).
//
// GET /events is the app-wide doorbell: a session-gated Server-Sent-Events
// stream that pushes a tiny topic string whenever the substrate emits a
// matching Postgres NOTIFY (see internal/live + migration 0025). It carries
// NO row data — a page that sees a topic it shows re-fetches its own data over
// a normal, independently-authorized GET. The stream is scoped per-user: an
// admin sees every topic, everyone else only topics for workspaces they belong
// to, so a topic id never leaks workspace existence to an unauthorized viewer.

package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/bobmcallan/satellites/internal/arbor"
)

// liveHeartbeatInterval bounds idle silence on the stream. EventSource and
// intermediaries treat a long-idle connection as dead; a periodic comment ping
// keeps it warm without sending a (meaningful) event.
const liveHeartbeatInterval = 25 * time.Second

func liveEventsHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := cfg.Sessions.UserID(r)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		scope, err := cfg.LiveScope(r.Context(), userID)
		if err != nil {
			http.Error(w, "could not resolve stream scope", http.StatusInternalServerError)
			return
		}

		client, cancel := cfg.Live.Subscribe(scope)
		defer cancel()

		scopeSummary := fmt.Sprintf("workspaces=%d", len(scope.Workspaces))
		if scope.Admin {
			scopeSummary = "admin"
		}
		arbor.Info("live: client connected", "user", userID, "scope", scopeSummary)
		defer arbor.Info("live: client disconnected", "user", userID)

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		fmt.Fprint(w, ": connected\n\n")
		flusher.Flush()

		heartbeat := time.NewTicker(liveHeartbeatInterval)
		defer heartbeat.Stop()
		var seq uint64

		for {
			select {
			case <-r.Context().Done():
				return
			case topic := <-client.C():
				seq++
				// id: lets the browser send Last-Event-ID on reconnect; we
				// don't replay (the page refetches on reconnect), so the id is
				// purely advisory for EventSource bookkeeping.
				fmt.Fprintf(w, "event: trigger\nid: %d\ndata: %s\n\n", seq, topic)
				flusher.Flush()
			case <-heartbeat.C:
				fmt.Fprint(w, ": ping\n\n")
				flusher.Flush()
			}
		}
	}
}

// liveStatsHandler is the session-gated diagnostic for the SSE bus
// (sty_2d01bb70): GET /events/stats returns the hub's in-memory counters
// (clients, published, delivered, dropped, last topic + time) as JSON so an
// operator can answer "did the trigger fire? did anyone receive it?" from the
// client, without trawling stderr. In-memory by design — see Hub.Stats.
func liveStatsHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, err := cfg.Sessions.UserID(r); err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(cfg.Live.Stats()); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}
