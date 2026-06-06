package workstate

import (
	"testing"
	"time"
)

// TestContextBaseline_SetAndReplace pins AC1/AC2: a sample can be recorded as
// the baseline, exactly one baseline exists at a time, and a new baseline
// replaces the old one.
func TestContextBaseline_SetAndReplace(t *testing.T) {
	s := openTemp(t)
	now := time.Unix(1700000000, 0).UTC()

	// No baseline initially.
	if _, ok, err := s.ContextBaseline(); err != nil || ok {
		t.Fatalf("fresh store: ok=%v err=%v, want ok=false", ok, err)
	}

	// A plain sample does not become the baseline.
	if _, err := s.RecordContextSample(ContextSample{Bytes: 8000, Tokens: 2000, TS: now}, false); err != nil {
		t.Fatalf("record sample: %v", err)
	}
	if _, ok, _ := s.ContextBaseline(); ok {
		t.Fatalf("a non-baseline sample must not set the baseline")
	}

	// Set a baseline.
	if _, err := s.RecordContextSample(ContextSample{Bytes: 8700, Tokens: 2180, TS: now.Add(time.Minute)}, true); err != nil {
		t.Fatalf("set baseline: %v", err)
	}
	b, ok, err := s.ContextBaseline()
	if err != nil || !ok || b.Bytes != 8700 {
		t.Fatalf("baseline = %+v ok=%v err=%v, want bytes=8700", b, ok, err)
	}

	// A newer baseline replaces it (exactly one baseline).
	if _, err := s.RecordContextSample(ContextSample{Bytes: 8000, Tokens: 2000, TS: now.Add(2 * time.Minute)}, true); err != nil {
		t.Fatalf("replace baseline: %v", err)
	}
	b2, _, _ := s.ContextBaseline()
	if b2.Bytes != 8000 {
		t.Fatalf("replaced baseline = %d, want 8000", b2.Bytes)
	}

	// History accrues over time (AC1: tracked metric over time).
	samples, err := s.ContextSamples(0)
	if err != nil {
		t.Fatalf("samples: %v", err)
	}
	if len(samples) != 3 {
		t.Fatalf("samples = %d, want 3 (all recorded)", len(samples))
	}
	// Exactly one is flagged baseline.
	flagged := 0
	for _, sm := range samples {
		if sm.IsBaseline {
			flagged++
		}
	}
	if flagged != 1 {
		t.Fatalf("baseline-flagged samples = %d, want exactly 1", flagged)
	}
}
