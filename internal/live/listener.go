package live

import (
	"encoding/json"
	"time"

	"github.com/bobmcallan/satellites/internal/arbor"
	"github.com/lib/pq"
)

// EventsChannel is the Postgres LISTEN/NOTIFY channel the substrate triggers
// emit on (see migration 0025_events_notify).
const EventsChannel = "satellites_events"

const (
	listenerMinReconnect = 10 * time.Second
	listenerMaxReconnect = time.Minute
	// listenerPingInterval probes an idle connection so a silently-dropped
	// TCP link is detected (pq then reconnects) rather than hanging forever.
	listenerPingInterval = 90 * time.Second
)

// Listener bridges Postgres NOTIFY into a Hub. One per server instance: it
// opens a dedicated pq connection (separate from the app's sql.DB pool),
// LISTENs on EventsChannel, decodes each payload, and republishes it to the
// hub. pq.Listener handles reconnect/backoff internally — every instance
// receives every NOTIFY, so fan-out is multi-instance safe for free.
type Listener struct {
	pq  *pq.Listener
	hub *Hub
}

// notifyPayload mirrors the json_build_object the satellites_notify SQL helper
// emits. p/w are authz scope ids; only t (topic) ever reaches a browser.
type notifyPayload struct {
	T string `json:"t"`
	P string `json:"p"`
	W string `json:"w"`
}

// NewListener opens a listener against dsn, establishes LISTEN, and starts the
// receive loop in a goroutine. It returns an error if the initial LISTEN
// fails; transient drops thereafter are handled by pq's reconnect.
func NewListener(dsn string, hub *Hub) (*Listener, error) {
	pl := pq.NewListener(dsn, listenerMinReconnect, listenerMaxReconnect,
		func(ev pq.ListenerEventType, err error) {
			if err != nil {
				arbor.Warn("live: listener connection event", "event", int(ev), "err", err)
				return
			}
			// Connected / disconnected / reconnected — low frequency, log at
			// Info so an operator can see the bus's link health (sty_2d01bb70).
			arbor.Info("live: listener connection event", "event", int(ev))
		})
	if err := pl.Listen(EventsChannel); err != nil {
		_ = pl.Close()
		return nil, err
	}
	arbor.Info("live: listener established", "channel", EventsChannel)
	l := &Listener{pq: pl, hub: hub}
	go l.run()
	return l, nil
}

func (l *Listener) run() {
	ping := time.NewTicker(listenerPingInterval)
	defer ping.Stop()
	for {
		select {
		case n, ok := <-l.pq.Notify:
			if !ok {
				return // listener closed
			}
			if n == nil {
				continue // reconnect event — no payload
			}
			var p notifyPayload
			if err := json.Unmarshal([]byte(n.Extra), &p); err != nil {
				arbor.Warn("live: undecodable notify payload", "extra", n.Extra, "err", err)
				continue
			}
			if p.T == "" {
				continue
			}
			arbor.Info("live: notify received", "topic", p.T)
			l.hub.Publish(Notification{Topic: p.T, ProjectID: p.P, WorkspaceID: p.W})
		case <-ping.C:
			// Ping can block on a dead socket; isolate it from the loop.
			go func() { _ = l.pq.Ping() }()
		}
	}
}

// Close stops the listener and releases its connection. The run goroutine
// exits when the Notify channel closes.
func (l *Listener) Close() error { return l.pq.Close() }
