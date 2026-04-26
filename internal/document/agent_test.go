package document

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const agentTestProject = "proj_agt"
const agentTestWorkspace = "ws_agt"

func seedConfigurationForAgent(t *testing.T, store *MemoryStore, name string, now time.Time) Document {
	t.Helper()
	payload, err := MarshalConfiguration(Configuration{})
	if err != nil {
		t.Fatalf("MarshalConfiguration: %v", err)
	}
	doc, err := store.Create(context.Background(), Document{
		WorkspaceID: agentTestWorkspace,
		ProjectID:   StringPtr(agentTestProject),
		Type:        TypeConfiguration,
		Name:        name,
		Body:        name + " bundle",
		Scope:       ScopeProject,
		Structured:  payload,
	}, now)
	if err != nil {
		t.Fatalf("seed configuration %q: %v", name, err)
	}
	return doc
}

func newAgentDoc(t *testing.T, name string, defaultCfgID string) Document {
	t.Helper()
	settings := AgentSettings{}
	if defaultCfgID != "" {
		v := defaultCfgID
		settings.DefaultConfigurationID = &v
	}
	payload, err := MarshalAgentSettings(settings)
	if err != nil {
		t.Fatalf("MarshalAgentSettings: %v", err)
	}
	return Document{
		WorkspaceID: agentTestWorkspace,
		ProjectID:   StringPtr(agentTestProject),
		Type:        TypeAgent,
		Name:        name,
		Body:        name + " agent body",
		Scope:       ScopeProject,
		Structured:  payload,
	}
}

func TestAgentSettings_MarshalRoundTrip(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   AgentSettings
	}{
		{"empty", AgentSettings{}},
		{"with default", AgentSettings{DefaultConfigurationID: ptr("doc_cfg_x")}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			payload, err := MarshalAgentSettings(tc.in)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			got, err := UnmarshalAgentSettings(payload)
			if err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if (got.DefaultConfigurationID == nil) != (tc.in.DefaultConfigurationID == nil) {
				t.Errorf("nil mismatch: got %v want %v", got.DefaultConfigurationID, tc.in.DefaultConfigurationID)
			}
			if got.DefaultConfigurationID != nil && tc.in.DefaultConfigurationID != nil &&
				*got.DefaultConfigurationID != *tc.in.DefaultConfigurationID {
				t.Errorf("value mismatch: got %q want %q", *got.DefaultConfigurationID, *tc.in.DefaultConfigurationID)
			}
		})
	}
}

func TestUnmarshalAgentSettings_EmptyPayloadReturnsZero(t *testing.T) {
	t.Parallel()
	got, err := UnmarshalAgentSettings(nil)
	if err != nil {
		t.Fatalf("nil payload: %v", err)
	}
	if got.DefaultConfigurationID != nil {
		t.Errorf("expected zero value; got %v", got.DefaultConfigurationID)
	}
	got2, err := UnmarshalAgentSettings([]byte{})
	if err != nil {
		t.Fatalf("empty payload: %v", err)
	}
	if got2.DefaultConfigurationID != nil {
		t.Errorf("expected zero value; got %v", got2.DefaultConfigurationID)
	}
}

func TestMemoryStore_CreateAgent_ValidDefaultConfigurationID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Now()
	cfg := seedConfigurationForAgent(t, store, "frontend", now)

	doc := newAgentDoc(t, "agent-A", cfg.ID)
	created, err := store.Create(ctx, doc, now)
	if err != nil {
		t.Fatalf("Create agent: %v", err)
	}
	settings, err := UnmarshalAgentSettings(created.Structured)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if settings.DefaultConfigurationID == nil || *settings.DefaultConfigurationID != cfg.ID {
		t.Errorf("DefaultConfigurationID = %v, want %q", settings.DefaultConfigurationID, cfg.ID)
	}
}

func TestMemoryStore_CreateAgent_DanglingDefaultConfigurationID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Now()

	doc := newAgentDoc(t, "agent-bad", "doc_does_not_exist")
	_, err := store.Create(ctx, doc, now)
	if err == nil {
		t.Fatal("Create with dangling default_configuration_id accepted; want rejection")
	}
	if !errors.Is(err, ErrDanglingAgentDefaultConfigurationID) {
		t.Errorf("err = %v, want ErrDanglingAgentDefaultConfigurationID", err)
	}
	if !strings.Contains(err.Error(), "doc_does_not_exist") {
		t.Errorf("error must name missing id; got %q", err.Error())
	}
}

func TestMemoryStore_CreateAgent_WrongTypeDefaultConfigurationID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Now()
	// Seed a contract (wrong type) and try to use its id as default_configuration_id.
	contractDoc, err := store.Create(ctx, Document{
		WorkspaceID: agentTestWorkspace,
		ProjectID:   StringPtr(agentTestProject),
		Type:        TypeContract,
		Name:        "develop",
		Body:        "develop",
		Scope:       ScopeProject,
	}, now)
	if err != nil {
		t.Fatalf("seed contract: %v", err)
	}

	doc := newAgentDoc(t, "agent-wrongtype", contractDoc.ID)
	_, err = store.Create(ctx, doc, now)
	if err == nil {
		t.Fatal("Create with wrong-type default_configuration_id accepted; want rejection")
	}
	if !errors.Is(err, ErrDanglingAgentDefaultConfigurationID) {
		t.Errorf("err = %v, want ErrDanglingAgentDefaultConfigurationID", err)
	}
	if !strings.Contains(err.Error(), "want type=configuration") {
		t.Errorf("error must name expected type; got %q", err.Error())
	}
}

func TestMemoryStore_UpdateAgent_DanglingDefaultConfigurationID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Now()
	cfg := seedConfigurationForAgent(t, store, "frontend", now)

	created, err := store.Create(ctx, newAgentDoc(t, "agent-A", cfg.ID), now)
	if err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	bad, _ := MarshalAgentSettings(AgentSettings{DefaultConfigurationID: ptr("doc_missing")})
	_, err = store.Update(ctx, created.ID, UpdateFields{Structured: &bad}, "test", now.Add(time.Minute), nil)
	if err == nil {
		t.Fatal("Update to dangling default_configuration_id accepted; want rejection")
	}
	if !errors.Is(err, ErrDanglingAgentDefaultConfigurationID) {
		t.Errorf("err = %v, want ErrDanglingAgentDefaultConfigurationID", err)
	}
}

func TestMemoryStore_ListAgentsByDefaultConfigurationID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Now()
	cfgA := seedConfigurationForAgent(t, store, "alpha", now)
	cfgB := seedConfigurationForAgent(t, store, "beta", now)

	a1, err := store.Create(ctx, newAgentDoc(t, "agent-1", cfgA.ID), now)
	if err != nil {
		t.Fatal(err)
	}
	a2, err := store.Create(ctx, newAgentDoc(t, "agent-2", cfgA.ID), now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	b1, err := store.Create(ctx, newAgentDoc(t, "agent-3", cfgB.ID), now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	noDefault, err := store.Create(ctx, newAgentDoc(t, "agent-4", ""), now.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	_ = noDefault

	matchedA, err := store.ListAgentsByDefaultConfigurationID(ctx, cfgA.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(matchedA) != 2 {
		t.Errorf("alpha matches: got %d, want 2", len(matchedA))
	}
	gotIDs := map[string]bool{}
	for _, d := range matchedA {
		gotIDs[d.ID] = true
	}
	if !gotIDs[a1.ID] || !gotIDs[a2.ID] {
		t.Errorf("alpha matches missing expected ids: got %v, want a1=%s + a2=%s", gotIDs, a1.ID, a2.ID)
	}

	matchedB, err := store.ListAgentsByDefaultConfigurationID(ctx, cfgB.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(matchedB) != 1 || matchedB[0].ID != b1.ID {
		t.Errorf("beta matches: got %v, want [%s]", matchedB, b1.ID)
	}

	none, _ := store.ListAgentsByDefaultConfigurationID(ctx, "doc_missing", nil)
	if len(none) != 0 {
		t.Errorf("missing-id matches: got %d, want 0", len(none))
	}
}

func TestMemoryStore_ListAgentsByDefaultConfigurationID_EmptyIDReturnsNil(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	got, err := store.ListAgentsByDefaultConfigurationID(context.Background(), "", nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for empty id; got %v", got)
	}
}

func ptr(s string) *string { return &s }
