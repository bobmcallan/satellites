package task

import "context"

// Listener is the workspace-agnostic task-bus subscriber surface
// (sty_c6d76a5b). Stores call OnEmit on every status transition
// (Enqueue / Claim / Publish / Close / Reclaim / Archive), after the
// existing per-workspace hub publish. Listeners receive every emitted
// row regardless of workspace — the embedded reviewer service is the
// canonical consumer; it filters Kind=review + IsSubscriberVisible
// statuses internally.
//
// Implementations must not block; the caller's mutation has already
// completed when OnEmit fires, but the call path waits for listeners
// to return. Panics are recovered by the store wrapper, so a buggy
// listener cannot abort the writer. Mirrors the pattern in
// internal/ledger/listener.go (sty_e805a01a).
type Listener interface {
	OnEmit(ctx context.Context, t Task)
}

// ListenerFunc adapts a plain function to Listener so callers can
// register a closure without defining a struct.
type ListenerFunc func(ctx context.Context, t Task)

// OnEmit implements Listener by invoking the underlying function.
func (f ListenerFunc) OnEmit(ctx context.Context, t Task) { f(ctx, t) }

// fanoutListeners invokes each listener with a per-call recover guard
// so a panic in one subscriber cannot abort the writer or starve other
// subscribers. Shared by both MemoryStore and SurrealStore.
func fanoutListeners(ctx context.Context, listeners []Listener, t Task) {
	for _, l := range listeners {
		if l == nil {
			continue
		}
		func(l Listener) {
			defer func() { _ = recover() }()
			l.OnEmit(ctx, t)
		}(l)
	}
}
