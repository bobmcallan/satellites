package arbor

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
)

// NewStderrHandler returns the same JSON/text slog.Handler arbor.New
// constructs internally, exposed so boot sites can include it as one
// child of a TeeHandler alongside the LedgerHandler.
func NewStderrHandler(level slog.Level, jsonOut bool, out io.Writer) slog.Handler {
	if out == nil {
		out = os.Stderr
	}
	opts := &slog.HandlerOptions{Level: level}
	if jsonOut {
		return slog.NewJSONHandler(out, opts)
	}
	return slog.NewTextHandler(out, opts)
}

// teeHandler fans one slog.Record out to multiple slog.Handler
// destinations. Used by SetHandlers to drive both the standard
// stderr/JSON sink and the auxiliary LedgerHandler the operator-
// observability path (sty_0006f5f5) registers at server boot.
//
// Failure isolation: a Handle error on any one child handler is
// collected but does not short-circuit the remaining children. The
// returned error joins each child's error via errors.Join.
type teeHandler struct {
	children []slog.Handler
}

// NewTeeHandler returns a Handler that fans records out to every
// supplied child. Nil children are filtered.
func NewTeeHandler(children ...slog.Handler) slog.Handler {
	out := make([]slog.Handler, 0, len(children))
	for _, c := range children {
		if c != nil {
			out = append(out, c)
		}
	}
	return &teeHandler{children: out}
}

// Enabled reports true when any child wants the record. A single
// noisy child does not silence the rest.
func (t *teeHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	for _, c := range t.children {
		if c.Enabled(ctx, lvl) {
			return true
		}
	}
	return false
}

// Handle dispatches to each child whose Enabled allows the level.
func (t *teeHandler) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, c := range t.children {
		if !c.Enabled(ctx, r.Level) {
			continue
		}
		// Each child needs its own Record clone — slog.Record.AddAttrs is
		// mutating and a child Handler is free to call it.
		clone := r.Clone()
		if err := c.Handle(ctx, clone); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (t *teeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return t
	}
	children := make([]slog.Handler, len(t.children))
	for i, c := range t.children {
		children[i] = c.WithAttrs(attrs)
	}
	return &teeHandler{children: children}
}

func (t *teeHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return t
	}
	children := make([]slog.Handler, len(t.children))
	for i, c := range t.children {
		children[i] = c.WithGroup(name)
	}
	return &teeHandler{children: children}
}

// SetHandlers replaces the process-global logger with one that fans
// out to every supplied handler. Called once at server boot after the
// LedgerHandler is constructed; the existing stderr / JSON handler
// is included alongside.
//
// The level passed here applies only to the new wrapper's filter
// surface; each child handler keeps its own Enabled behaviour.
func SetHandlers(level slog.Level, handlers ...slog.Handler) {
	tee := NewTeeHandler(handlers...)
	SetDefault(&Logger{inner: slog.New(tee)})
	_ = level // reserved for future per-wrapper filtering
}
