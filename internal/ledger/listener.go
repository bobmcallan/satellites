package ledger

import "context"

// Listener is the workspace-agnostic ledger-bus subscriber surface.
// Stores call OnAppend on every successful Append, after the existing
// per-workspace hub publish. Listeners receive every appended row
// regardless of workspace — the substrate-wide reconciler in
// internal/storystatus is the canonical consumer (sty_e805a01a).
//
// Implementations must not block; the caller's mutation has already
// completed when OnAppend fires, but the Append return path waits for
// listeners to return. Panics are recovered by the store wrapper, so
// a buggy listener cannot abort the writer.
type Listener interface {
	OnAppend(ctx context.Context, entry LedgerEntry)
}

// ListenerFunc adapts a plain function to Listener so callers can
// register a closure without defining a struct.
type ListenerFunc func(ctx context.Context, entry LedgerEntry)

// OnAppend implements Listener by invoking the underlying function.
func (f ListenerFunc) OnAppend(ctx context.Context, entry LedgerEntry) { f(ctx, entry) }

// fanoutListeners invokes each listener with a per-call recover guard
// so a panic in one subscriber cannot abort the writer or starve other
// subscribers. Shared by both MemoryStore and SurrealStore.
func fanoutListeners(ctx context.Context, listeners []Listener, entry LedgerEntry) {
	for _, l := range listeners {
		if l == nil {
			continue
		}
		func(l Listener) {
			defer func() { _ = recover() }()
			l.OnAppend(ctx, entry)
		}(l)
	}
}
