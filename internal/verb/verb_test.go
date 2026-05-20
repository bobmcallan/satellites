package verb

import (
	"context"
	"encoding/json"
	"testing"
)

func TestDispatch_Version(t *testing.T) {
	resp, err := Dispatch(context.Background(), "version", nil)
	if err != nil {
		t.Fatalf("dispatch version: %v", err)
	}
	var info VersionInfo
	if err := json.Unmarshal(resp, &info); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if info.Version == "" || info.Commit == "" || info.BuildTime == "" {
		t.Fatalf("empty fields in version response: %+v", info)
	}
}

func TestDispatch_Unknown(t *testing.T) {
	if _, err := Dispatch(context.Background(), "no_such_verb", nil); err == nil {
		t.Fatal("expected error for unknown verb, got nil")
	}
}

func TestCatalog_ContainsBuiltins(t *testing.T) {
	catalog := Catalog()
	want := map[string]bool{
		"version":           false,
		"session_bootstrap": false,
		"auth":              false,
		"verb_discovery":    false,
	}
	for _, n := range catalog {
		if _, ok := want[n]; ok {
			want[n] = true
		}
	}
	for n, found := range want {
		if !found {
			t.Errorf("catalog missing built-in %q (got %v)", n, catalog)
		}
	}
}

func TestVerbDiscovery_ReturnsCatalog(t *testing.T) {
	resp, err := Dispatch(context.Background(), "verb_discovery", nil)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	var cat VerbCatalog
	if err := json.Unmarshal(resp, &cat); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(cat.Verbs) == 0 {
		t.Fatal("verb_discovery returned empty catalog")
	}
	// Catalog from the verb must equal Catalog() directly.
	direct := Catalog()
	if len(cat.Verbs) != len(direct) {
		t.Fatalf("len mismatch: verb=%d direct=%d", len(cat.Verbs), len(direct))
	}
	for i := range cat.Verbs {
		if cat.Verbs[i] != direct[i] {
			t.Fatalf("catalog drift at %d: verb=%q direct=%q", i, cat.Verbs[i], direct[i])
		}
	}
}
