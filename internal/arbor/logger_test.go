package arbor

import (
	"context"
	"testing"
)

func TestRequestIDRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if got := RequestIDFrom(ctx); got != "" {
		t.Fatalf("expected empty request id on fresh context, got %q", got)
	}

	ctx = WithRequestID(ctx, "req_abc123")
	if got := RequestIDFrom(ctx); got != "req_abc123" {
		t.Fatalf("expected %q, got %q", "req_abc123", got)
	}
}

func TestDefaultLoggerNotNil(t *testing.T) {
	t.Parallel()
	if Default() == nil {
		t.Fatal("Default() must return a non-nil logger")
	}
}

func TestNewRespectsLevel(t *testing.T) {
	t.Parallel()
	if New("debug") == nil {
		t.Fatal("New(debug) must return a non-nil logger")
	}
	if New("garbage") == nil {
		t.Fatal("New(unknown level) must still return a logger")
	}
}
