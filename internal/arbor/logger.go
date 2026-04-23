// Package arbor wraps the ternarybob/arbor structured logger with satellites-v4
// defaults: stdout-only console writer (Fly captures stdout), configurable
// level, and context helpers for request-id propagation.
package arbor

import (
	"context"
	"os"
	"sync"

	"github.com/ternarybob/arbor"
	arbormodels "github.com/ternarybob/arbor/models"
)

type ctxKey int

const requestIDKey ctxKey = iota

var (
	defaultOnce sync.Once
	defaultLog  arbor.ILogger
)

// Default returns a process-wide arbor logger with level "info" and a console
// writer on stdout. Safe for use before config load (boot-time errors).
func Default() arbor.ILogger {
	defaultOnce.Do(func() {
		defaultLog = New("info")
	})
	return defaultLog
}

// New builds an arbor logger at the given level with a console writer on
// stdout. Level strings parseable by arbor include "trace", "debug", "info",
// "warn", "error". Unknown levels fall back to arbor's own default.
func New(level string) arbor.ILogger {
	return arbor.NewLogger().
		WithConsoleWriter(arbormodels.WriterConfiguration{
			Type:       arbormodels.LogWriterTypeConsole,
			Writer:     os.Stdout,
			TimeFormat: "2006-01-02T15:04:05Z07:00",
		}).
		WithLevelFromString(level)
}

// WithRequestID attaches a request ID to ctx; downstream code pulls it out via
// RequestIDFrom and attaches it as a structured field on every log line.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestIDFrom returns the request ID stored on ctx, or "" if none.
func RequestIDFrom(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey).(string)
	return v
}
