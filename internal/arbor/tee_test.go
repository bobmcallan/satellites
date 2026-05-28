package arbor

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"
)

type recordingHandler struct {
	records []slog.Record
	err     error
	enabled bool
}

func (h *recordingHandler) Enabled(_ context.Context, _ slog.Level) bool {
	return h.enabled
}
func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r)
	return h.err
}
func (h *recordingHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(_ string) slog.Handler      { return h }

func TestTeeHandler_FanOutToEnabledChildren(t *testing.T) {
	a := &recordingHandler{enabled: true}
	b := &recordingHandler{enabled: true}
	c := &recordingHandler{enabled: false}
	tee := NewTeeHandler(a, b, c)

	r := slog.NewRecord(time.Now(), slog.LevelInfo, "hello", 0)
	if err := tee.Handle(context.Background(), r); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(a.records) != 1 {
		t.Fatalf("child a: got %d records", len(a.records))
	}
	if len(b.records) != 1 {
		t.Fatalf("child b: got %d records", len(b.records))
	}
	if len(c.records) != 0 {
		t.Fatalf("disabled child c: got %d records", len(c.records))
	}
}

func TestTeeHandler_OneChildErrorDoesNotShortCircuit(t *testing.T) {
	a := &recordingHandler{enabled: true, err: errors.New("a failed")}
	b := &recordingHandler{enabled: true}
	tee := NewTeeHandler(a, b)

	r := slog.NewRecord(time.Now(), slog.LevelInfo, "msg", 0)
	err := tee.Handle(context.Background(), r)
	if err == nil {
		t.Fatal("expected error")
	}
	if len(b.records) != 1 {
		t.Fatalf("b should still receive record: got %d", len(b.records))
	}
}

func TestTeeHandler_EnabledAggregates(t *testing.T) {
	off := &recordingHandler{enabled: false}
	on := &recordingHandler{enabled: true}
	tee := NewTeeHandler(off, on)
	if !tee.Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("expected Enabled true when any child is on")
	}

	bothOff := NewTeeHandler(off, &recordingHandler{enabled: false})
	if bothOff.Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("expected Enabled false when all children off")
	}
}
