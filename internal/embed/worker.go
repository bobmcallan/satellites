package embed

import (
	"context"
	"time"

	"github.com/bobmcallan/satellites/internal/arbor"
)

// Worker reconciles the workspace corpus on an interval — the background
// "embed job" mechanism (AC1). Started as a goroutine in cmd/satellites-server
// when a Gemini key is present; runs until its context is cancelled.
type Worker struct {
	svc      *Service
	interval time.Duration
}

// NewWorker builds a worker; a non-positive interval falls back to 30s.
func NewWorker(svc *Service, interval time.Duration) *Worker {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &Worker{svc: svc, interval: interval}
}

// Run does an initial reconcile then repeats every interval until ctx is done.
func (w *Worker) Run(ctx context.Context) {
	w.once(ctx)
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.once(ctx)
		}
	}
}

func (w *Worker) once(ctx context.Context) {
	if err := w.svc.Reconcile(ctx); err != nil {
		arbor.WarnCtx(ctx, "embed: reconcile pass", "err", err)
	}
}
