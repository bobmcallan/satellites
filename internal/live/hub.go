package live

import "sync"

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
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		if !c.scope.Allows(n) {
			continue
		}
		select {
		case c.ch <- n.Topic:
		default:
		}
	}
}

// ClientCount reports the number of subscribed clients (telemetry / tests).
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}
