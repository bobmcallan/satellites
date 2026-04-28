package kvtemplate

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/ledger"
)

// staticResolver is a Resolver backed by a fixed map for unit testing.
type staticResolver struct {
	values map[string]string
	calls  map[string]int
	err    error
}

func (s *staticResolver) Resolve(_ context.Context, key string) (string, bool, error) {
	if s.err != nil {
		return "", false, s.err
	}
	if s.calls == nil {
		s.calls = make(map[string]int)
	}
	s.calls[key]++
	v, ok := s.values[key]
	return v, ok, nil
}

func TestRender_AllKeysPresent(t *testing.T) {
	t.Parallel()
	r := &staticResolver{values: map[string]string{"NAME": "alpha", "REGION": "us-east"}}
	got, err := Render(context.Background(), "hello {{NAME}} from {{REGION}}", r)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got.Text != "hello alpha from us-east" {
		t.Errorf("Text = %q, want %q", got.Text, "hello alpha from us-east")
	}
	if len(got.Unresolved) != 0 {
		t.Errorf("Unresolved = %v, want empty", got.Unresolved)
	}
}

func TestRender_UnresolvedKey(t *testing.T) {
	t.Parallel()
	r := &staticResolver{values: map[string]string{"NAME": "alpha"}}
	got, err := Render(context.Background(), "hi {{NAME}} ({{TENANT}})", r)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(got.Text, "{{TENANT}}") {
		t.Errorf("Text = %q, want unresolved placeholder preserved", got.Text)
	}
	if len(got.Unresolved) != 1 || got.Unresolved[0] != "TENANT" {
		t.Errorf("Unresolved = %v, want [TENANT]", got.Unresolved)
	}
}

func TestRender_NoPlaceholders(t *testing.T) {
	t.Parallel()
	r := &staticResolver{}
	got, err := Render(context.Background(), "plain text", r)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got.Text != "plain text" {
		t.Errorf("Text = %q, want unchanged", got.Text)
	}
	if len(got.Unresolved) != 0 {
		t.Errorf("Unresolved = %v, want empty", got.Unresolved)
	}
}

func TestRender_RepeatedKeyResolvedOnce(t *testing.T) {
	t.Parallel()
	r := &staticResolver{values: map[string]string{"X": "yes"}}
	got, err := Render(context.Background(), "{{X}} {{X}} {{X}}", r)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got.Text != "yes yes yes" {
		t.Errorf("Text = %q, want yes yes yes", got.Text)
	}
	if r.calls["X"] != 1 {
		t.Errorf("resolver called %d times for X, want 1 (cached)", r.calls["X"])
	}
}

func TestRender_Idempotent(t *testing.T) {
	t.Parallel()
	r := &staticResolver{values: map[string]string{"K": "v"}}
	first, _ := Render(context.Background(), "use {{K}}", r)
	second, _ := Render(context.Background(), first.Text, r)
	if first.Text != second.Text {
		t.Errorf("non-idempotent: first=%q second=%q", first.Text, second.Text)
	}
}

func TestRender_DuplicateUnresolvedDeduped(t *testing.T) {
	t.Parallel()
	r := &staticResolver{}
	got, _ := Render(context.Background(), "{{X}} {{Y}} {{X}}", r)
	if len(got.Unresolved) != 2 {
		t.Errorf("Unresolved = %v, want 2 unique keys", got.Unresolved)
	}
}

func TestRender_ResolverError(t *testing.T) {
	t.Parallel()
	r := &staticResolver{err: errors.New("boom")}
	if _, err := Render(context.Background(), "{{K}}", r); err == nil {
		t.Fatal("expected propagated resolver error")
	}
}

func TestLedgerResolver_ResolvesViaKVChain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := ledger.NewMemoryStore()
	t0 := time.Now().UTC()

	// Seed system + project values; the chain should pick system per
	// the KVResolveScoped precedence (system always wins).
	_, _ = store.Append(ctx, ledger.LedgerEntry{Type: ledger.TypeKV, Tags: []string{"scope:system", "key:env"}, Content: "production"}, t0)
	_, _ = store.Append(ctx, ledger.LedgerEntry{WorkspaceID: "ws_1", ProjectID: "proj_a", Type: ledger.TypeKV, Tags: []string{"scope:project", "key:env"}, Content: "dev"}, t0)

	resolver := LedgerResolver{
		Store:       store,
		Opts:        ledger.KVResolveOptions{WorkspaceID: "ws_1", ProjectID: "proj_a"},
		Memberships: []string{"", "ws_1"},
	}
	got, err := Render(ctx, "running in {{env}}", resolver)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got.Text != "running in production" {
		t.Errorf("Text = %q, want production (system wins)", got.Text)
	}
}
