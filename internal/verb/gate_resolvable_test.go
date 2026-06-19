package verb

import (
	"context"
	"testing"
)

// TestGateResolvable pins sty_f242eacf: the validator predicate agrees with the
// dispatcher's embed → local → server resolution. A gate is resolvable when it
// is embedded in the binary, OR (on a local miss) fetched from the server; it is
// unresolvable only when no tier holds it. A nil fetch reduces it to embed →
// local.
func TestGateResolvable(t *testing.T) {
	// An embedded internal gate resolves without any fetch (embed tier).
	if !GateResolvable(context.Background(), nil, t.TempDir(), "satellites-intent-plan-review") {
		t.Fatal("embedded internal gate must resolve via the embed tier")
	}

	body := "---\nname: satellites-story-plan-review\nkind: gate\n---\n# rubric\n"
	fetch := func(_ context.Context, name string) ([]byte, bool, error) {
		if name == "satellites-story-plan-review" {
			return []byte(body), true, nil
		}
		return nil, false, nil
	}

	// A substrate gate absent locally but held by the server resolves via fetch.
	if !GateResolvable(context.Background(), fetch, t.TempDir(), "satellites-story-plan-review") {
		t.Fatal("server-held gate must resolve via the server tier on a local miss")
	}

	// No tier holds the name → not resolvable.
	if GateResolvable(context.Background(), fetch, t.TempDir(), "satellites-nonexistent-gate") {
		t.Fatal("a gate no tier holds must be unresolvable")
	}

	// Nil fetch + local miss → not resolvable (fails closed, no server tier).
	if GateResolvable(context.Background(), nil, t.TempDir(), "satellites-story-plan-review") {
		t.Fatal("nil fetch must leave a non-embedded, locally-absent gate unresolvable")
	}
}
