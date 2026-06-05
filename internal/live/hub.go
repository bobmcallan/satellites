package live

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/bobmcallan/satellites/internal/arbor"
)

// clientBuffer is how many pending topics a slow client may queue before
// further triggers are dropped for it. A drop is harmless: the browser
// re-fetches on the next trigger it does receive (and on reconnect), so it
// converges to current — the bus is a doorbell, not a transactional log.
const clientBuffer = 64

// Client is one subscribed SSE connection. The owning handler ranges C() for
// delivered topics and calls the cancel func returned by Subscribe on
// disconnect.
type Client struct {
	scope Scope
	ch    chan string
}

// C is the receive channel of delivered topics for this client.
func (c *Client) C() <-chan string { return c.ch }

// Hub fans Notifications out to authorized, subscribed clients. Safe for
// concurrent use; one Hub per server instance.
type Hub struct {
	mu      sync.RWMutex
	clients map[*Client]struct{}

	// Observability counters (sty_2d01bb70) — in-memory only. Writing bus
	// signal to the evidence ledger would re-fire the bus's own NOTIFY
	// trigger (a feedback loop), so the diagnostic surface is a snapshot read
	// via GET /events/stats, never a ledger row.
	published atomic.Uint64 // notifications fanned in (one per NOTIFY)
	delivered atomic.Uint64 // topic sends to authorized, non-full clients
	dropped   atomic.Uint64 // sends skipped because a client buffer was full

	lastMu    sync.Mutex
	lastTopic string
	lastAt    time.Time
}

// Stats is a point-in-time snapshot of bus activity for the diagnostic route.
type Stats struct {
	Clients      int    `json:"clients"`
	Published    uint64 `json:"published"`
	Delivered    uint64 `json:"delivered"`
	Dropped      uint64 `json:"dropped"`
	LastTopic    string `json:"last_topic"`
	LastUnixNano int64  `json:"last_unix_nano"`
}

// Stats returns a snapshot of the hub's counters and last-notify marker.
func (h *Hub) Stats() Stats {
	h.lastMu.Lock()
	topic, at := h.lastTopic, h.lastAt
	h.lastMu.Unlock()
	var nano int64
	if !at.IsZero() {
		nano = at.UnixNano()
	}
	return Stats{
		Clients:      h.ClientCount(),
		Published:    h.published.Load(),
		Delivered:    h.delivered.Load(),
		Dropped:      h.dropped.Load(),
		LastTopic:    topic,
		LastUnixNano: nano,
	}
}

// NewHub returns an empty hub ready to Subscribe/Publish.
func NewHub() *Hub {
	return &Hub{clients: make(map[*Client]struct{})}
}

// Subscribe registers a client with the given scope and returns it plus a
// cancel func that unsubscribes it. The handler must call cancel on disconnect.
func (h *Hub) Subscribe(scope Scope) (*Client, func()) {
	c := &Client{scope: scope, ch: make(chan string, clientBuffer)}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	var once sync.Once
	return c, func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.clients, c)
			h.mu.Unlock()
		})
	}
}

// Publish delivers n's topic to every authorized subscriber. Non-blocking: a
// client whose buffer is full drops this trigger rather than stalling the
// publish path (it refetches on the next one).
func (h *Hub) Publish(n Notification) {
	h.published.Add(1)
	h.lastMu.Lock()
	h.lastTopic = n.Topic
	h.lastAt = time.Now()
	h.lastMu.Unlock()

	recipients, dropped := 0, 0
	h.mu.RLock()
	for c := range h.clients {
		if !c.scope.Allows(n) {
			continue
		}
		select {
		case c.ch <- n.Topic:
			recipients++
		default:
			dropped++
		}
	}
	h.mu.RUnlock()

	if recipients > 0 {
		h.delivered.Add(uint64(recipients))
	}
	if dropped > 0 {
		h.dropped.Add(uint64(dropped))
		arbor.Warn("live: trigger dropped (client buffer full)", "topic", n.Topic, "dropped", dropped)
	}
	arbor.Debug("live: publish", "topic", n.Topic, "recipients", recipients, "dropped", dropped)
}

// ClientCount reports the number of subscribed clients (telemetry / tests).
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}
