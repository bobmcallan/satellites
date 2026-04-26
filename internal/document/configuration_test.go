package document

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const cfgTestProject = "proj_cfg"
const cfgTestWorkspace = "ws_cfg"

func TestConfiguration_MarshalRoundTrip(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   Configuration
	}{
		{
			name: "empty",
			in:   Configuration{},
		},
		{
			name: "ordered contract refs preserved",
			in: Configuration{
				ContractRefs: []string{"doc_c1", "doc_c2", "doc_c3"},
			},
		},
		{
			name: "all three ref kinds populated",
			in: Configuration{
				ContractRefs:  []string{"doc_c1", "doc_c2"},
				SkillRefs:     []string{"doc_s1"},
				PrincipleRefs: []string{"doc_p1", "doc_p2"},
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			payload, err := MarshalConfiguration(tc.in)
			if err != nil {
				t.Fatalf("MarshalConfiguration: %v", err)
			}
			got, err := UnmarshalConfiguration(payload)
			if err != nil {
				t.Fatalf("UnmarshalConfiguration: %v", err)
			}
			if !equalStringSlice(got.ContractRefs, defaultEmpty(tc.in.ContractRefs)) {
				t.Errorf("contract_refs: got %v, want %v", got.ContractRefs, tc.in.ContractRefs)
			}
			if !equalStringSlice(got.SkillRefs, defaultEmpty(tc.in.SkillRefs)) {
				t.Errorf("skill_refs: got %v, want %v", got.SkillRefs, tc.in.SkillRefs)
			}
			if !equalStringSlice(got.PrincipleRefs, defaultEmpty(tc.in.PrincipleRefs)) {
				t.Errorf("principle_refs: got %v, want %v", got.PrincipleRefs, tc.in.PrincipleRefs)
			}
		})
	}
}

func TestUnmarshalConfiguration_EmptyPayloadRejected(t *testing.T) {
	t.Parallel()
	if _, err := UnmarshalConfiguration(nil); err == nil {
		t.Errorf("nil payload accepted; want error")
	}
	if _, err := UnmarshalConfiguration([]byte{}); err == nil {
		t.Errorf("empty payload accepted; want error")
	}
}

func TestUnmarshalConfiguration_MalformedJSONRejected(t *testing.T) {
	t.Parallel()
	if _, err := UnmarshalConfiguration([]byte("not json")); err == nil {
		t.Errorf("malformed payload accepted; want error")
	}
}

// seedRefs creates one contract, one skill, one principle in store and
// returns their ids. Used as the happy-path ref set for configuration
// tests.
func seedRefs(t *testing.T, store *MemoryStore, now time.Time) (contractID, skillID, principleID string) {
	t.Helper()
	ctx := context.Background()
	contract, err := store.Create(ctx, Document{
		WorkspaceID: cfgTestWorkspace,
		ProjectID:   StringPtr(cfgTestProject),
		Type:        TypeContract,
		Name:        "develop",
		Body:        "develop contract body",
		Scope:       ScopeProject,
	}, now)
	if err != nil {
		t.Fatalf("seed contract: %v", err)
	}
	skill, err := store.Create(ctx, Document{
		WorkspaceID:     cfgTestWorkspace,
		ProjectID:       StringPtr(cfgTestProject),
		Type:            TypeSkill,
		Name:            "golang-testing",
		Body:            "skill body",
		Scope:           ScopeProject,
		ContractBinding: StringPtr(contract.ID),
	}, now)
	if err != nil {
		t.Fatalf("seed skill: %v", err)
	}
	principle, err := store.Create(ctx, Document{
		WorkspaceID: cfgTestWorkspace,
		ProjectID:   StringPtr(cfgTestProject),
		Type:        TypePrinciple,
		Name:        "pr_local_iteration",
		Body:        "principle body",
		Scope:       ScopeProject,
	}, now)
	if err != nil {
		t.Fatalf("seed principle: %v", err)
	}
	return contract.ID, skill.ID, principle.ID
}

func newConfigurationDoc(t *testing.T, cfg Configuration) Document {
	t.Helper()
	payload, err := MarshalConfiguration(cfg)
	if err != nil {
		t.Fatalf("MarshalConfiguration: %v", err)
	}
	return Document{
		WorkspaceID: cfgTestWorkspace,
		ProjectID:   StringPtr(cfgTestProject),
		Type:        TypeConfiguration,
		Name:        "frontend-config",
		Body:        "frontend configuration description",
		Scope:       ScopeProject,
		Structured:  payload,
	}
}

func TestMemoryStore_CreateConfiguration_Valid(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Now()
	contractID, skillID, principleID := seedRefs(t, store, now)

	doc := newConfigurationDoc(t, Configuration{
		ContractRefs:  []string{contractID},
		SkillRefs:     []string{skillID},
		PrincipleRefs: []string{principleID},
	})
	created, err := store.Create(ctx, doc, now)
	if err != nil {
		t.Fatalf("Create configuration: %v", err)
	}
	if created.Type != TypeConfiguration {
		t.Errorf("type = %q, want %q", created.Type, TypeConfiguration)
	}
	cfg, err := UnmarshalConfiguration(created.Structured)
	if err != nil {
		t.Fatalf("UnmarshalConfiguration: %v", err)
	}
	if len(cfg.ContractRefs) != 1 || cfg.ContractRefs[0] != contractID {
		t.Errorf("contract_refs = %v, want [%q]", cfg.ContractRefs, contractID)
	}
}

func TestMemoryStore_CreateConfiguration_DanglingContractRef(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Now()

	doc := newConfigurationDoc(t, Configuration{
		ContractRefs: []string{"doc_does_not_exist"},
	})
	_, err := store.Create(ctx, doc, now)
	if err == nil {
		t.Fatalf("Create with dangling contract ref accepted; want rejection")
	}
	if !errors.Is(err, ErrDanglingConfigurationRef) {
		t.Errorf("err = %v, want ErrDanglingConfigurationRef", err)
	}
	if !strings.Contains(err.Error(), "doc_does_not_exist") {
		t.Errorf("error message must name the missing id; got %q", err.Error())
	}
}

func TestMemoryStore_CreateConfiguration_WrongTypeRef(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Now()
	_, skillID, _ := seedRefs(t, store, now)

	// Pass the skill id where a contract id is expected.
	doc := newConfigurationDoc(t, Configuration{
		ContractRefs: []string{skillID},
	})
	_, err := store.Create(ctx, doc, now)
	if err == nil {
		t.Fatalf("Create with wrong-type contract ref accepted; want rejection")
	}
	if !errors.Is(err, ErrDanglingConfigurationRef) {
		t.Errorf("err = %v, want ErrDanglingConfigurationRef", err)
	}
	if !strings.Contains(err.Error(), "want type=contract") {
		t.Errorf("error message must name the expected type; got %q", err.Error())
	}
}

func TestMemoryStore_CreateConfiguration_DanglingSkillRef(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Now()
	contractID, _, _ := seedRefs(t, store, now)

	doc := newConfigurationDoc(t, Configuration{
		ContractRefs: []string{contractID},
		SkillRefs:    []string{"doc_missing_skill"},
	})
	_, err := store.Create(ctx, doc, now)
	if err == nil {
		t.Fatalf("Create with dangling skill ref accepted; want rejection")
	}
	if !errors.Is(err, ErrDanglingConfigurationRef) {
		t.Errorf("err = %v, want ErrDanglingConfigurationRef", err)
	}
}

func TestMemoryStore_CreateConfiguration_DanglingPrincipleRef(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Now()
	contractID, _, _ := seedRefs(t, store, now)

	doc := newConfigurationDoc(t, Configuration{
		ContractRefs:  []string{contractID},
		PrincipleRefs: []string{"doc_missing_principle"},
	})
	_, err := store.Create(ctx, doc, now)
	if err == nil {
		t.Fatalf("Create with dangling principle ref accepted; want rejection")
	}
	if !errors.Is(err, ErrDanglingConfigurationRef) {
		t.Errorf("err = %v, want ErrDanglingConfigurationRef", err)
	}
}

func TestMemoryStore_UpdateConfiguration_DanglingRef(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Now()
	contractID, _, _ := seedRefs(t, store, now)

	created, err := store.Create(ctx, newConfigurationDoc(t, Configuration{
		ContractRefs: []string{contractID},
	}), now)
	if err != nil {
		t.Fatalf("seed configuration: %v", err)
	}

	bad, err := MarshalConfiguration(Configuration{
		ContractRefs: []string{contractID, "doc_missing"},
	})
	if err != nil {
		t.Fatalf("MarshalConfiguration: %v", err)
	}
	_, err = store.Update(ctx, created.ID, UpdateFields{Structured: &bad}, "test", now.Add(time.Minute), nil)
	if err == nil {
		t.Fatalf("Update to dangling ref accepted; want rejection")
	}
	if !errors.Is(err, ErrDanglingConfigurationRef) {
		t.Errorf("err = %v, want ErrDanglingConfigurationRef", err)
	}
}

func TestMemoryStore_CreateConfiguration_BadScope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Now()

	doc := newConfigurationDoc(t, Configuration{})
	doc.Scope = ScopeSystem
	doc.ProjectID = nil
	_, err := store.Create(ctx, doc, now)
	if err == nil {
		t.Fatalf("Create with scope=system accepted; want rejection")
	}
	if !strings.Contains(err.Error(), "scope=project") {
		t.Errorf("error message must name the required scope; got %q", err.Error())
	}
}

func TestMemoryStore_CreateConfiguration_NilStructured(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Now()

	doc := Document{
		WorkspaceID: cfgTestWorkspace,
		ProjectID:   StringPtr(cfgTestProject),
		Type:        TypeConfiguration,
		Name:        "frontend-config",
		Body:        "description",
		Scope:       ScopeProject,
		// Structured intentionally nil
	}
	_, err := store.Create(ctx, doc, now)
	if err == nil {
		t.Fatalf("Create with nil Structured accepted; want rejection")
	}
	if !strings.Contains(err.Error(), "non-empty structured payload") {
		t.Errorf("error message must mention the missing payload; got %q", err.Error())
	}
}

func TestMemoryStore_CreateConfiguration_ContractBindingForbidden(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Now()
	contractID, _, _ := seedRefs(t, store, now)

	doc := newConfigurationDoc(t, Configuration{ContractRefs: []string{contractID}})
	doc.ContractBinding = StringPtr(contractID)
	_, err := store.Create(ctx, doc, now)
	if err == nil {
		t.Fatalf("Create configuration with ContractBinding accepted; want rejection")
	}
}

func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func defaultEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
