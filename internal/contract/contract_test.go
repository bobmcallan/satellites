package contract

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/story"
)

const (
	wsA = "wksp_a"
	wsB = "wksp_b"
)

// newFixture wires a contract.MemoryStore backed by document +
// story + ledger memory stores with one active system-scope contract
// doc and one parent story in wsA.
type fixture struct {
	ctx         context.Context
	now         time.Time
	docs        *document.MemoryStore
	stories     *story.MemoryStore
	ledger      *ledger.MemoryStore
	contracts   *MemoryStore
	contractDoc document.Document
	parentStory story.Story
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	now := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)
	docs := document.NewMemoryStore()
	led := ledger.NewMemoryStore()
	stories := story.NewMemoryStore(led)
	contracts := NewMemoryStore(docs, stories)

	// Seed one active system-scope contract doc. Use Create with an
	// explicit id so tests can cite a predictable FK target.
	contractDoc, err := docs.Create(context.Background(), document.Document{
		ID:     "doc_test_contract",
		Type:   document.TypeContract,
		Scope:  document.ScopeSystem,
		Name:   "test_contract",
		Body:   "contract body",
		Status: document.StatusActive,
	}, now)
	if err != nil {
		t.Fatalf("seed contract doc: %v", err)
	}

	parent, err := stories.Create(context.Background(), story.Story{
		WorkspaceID: wsA,
		ProjectID:   "proj_A",
		Title:       "parent",
	}, now)
	if err != nil {
		t.Fatalf("seed parent story: %v", err)
	}

	return &fixture{
		ctx:         context.Background(),
		now:         now,
		docs:        docs,
		stories:     stories,
		ledger:      led,
		contracts:   contracts,
		contractDoc: contractDoc,
		parentStory: parent,
	}
}

func TestNewID_Format(t *testing.T) {
	t.Parallel()
	id := NewID()
	if !strings.HasPrefix(id, "ci_") {
		t.Fatalf("NewID: prefix: %q", id)
	}
	if len(id) != len("ci_")+8 {
		t.Fatalf("NewID: length: %q", id)
	}
}

func TestValidTransition(t *testing.T) {
	t.Parallel()
	cases := []struct {
		from, to string
		ok       bool
	}{
		{StatusReady, StatusClaimed, true},
		{StatusReady, StatusSkipped, true},
		{StatusReady, StatusPassed, false},
		{StatusReady, StatusFailed, false},
		{StatusReady, StatusReady, false},
		{StatusClaimed, StatusPassed, true},
		{StatusClaimed, StatusFailed, true},
		{StatusClaimed, StatusSkipped, true},
		{StatusClaimed, StatusReady, false},
		{StatusClaimed, StatusClaimed, false},
		{StatusPassed, StatusClaimed, false},
		{StatusPassed, StatusFailed, false},
		{StatusFailed, StatusClaimed, false},
		{StatusSkipped, StatusClaimed, false},
		{"bogus", StatusReady, false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.from+"_to_"+c.to, func(t *testing.T) {
			t.Parallel()
			if got := ValidTransition(c.from, c.to); got != c.ok {
				t.Fatalf("ValidTransition(%q,%q): got %v want %v", c.from, c.to, got, c.ok)
			}
		})
	}
}

func TestCreate_CascadesFromStory(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ci, err := f.contracts.Create(f.ctx, ContractInstance{
		// Caller-supplied values MUST be ignored.
		WorkspaceID:      "wksp_bogus",
		ProjectID:        "proj_bogus",
		StoryID:          f.parentStory.ID,
		ContractID:       f.contractDoc.ID,
		ContractName:     "test_contract",
		Sequence:         0,
		RequiredForClose: true,
	}, f.now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ci.WorkspaceID != wsA {
		t.Fatalf("workspace cascade: got %q want %q", ci.WorkspaceID, wsA)
	}
	if ci.ProjectID != "proj_A" {
		t.Fatalf("project cascade: got %q want %q", ci.ProjectID, "proj_A")
	}
	if ci.Status != StatusReady {
		t.Fatalf("default status: got %q want %q", ci.Status, StatusReady)
	}
	if !strings.HasPrefix(ci.ID, "ci_") {
		t.Fatalf("id prefix: %q", ci.ID)
	}
}

func TestCreate_RejectsInvalidFK(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	_, err := f.contracts.Create(f.ctx, ContractInstance{
		StoryID:      f.parentStory.ID,
		ContractID:   "doc_does_not_exist",
		ContractName: "ghost",
	}, f.now)
	if !errors.Is(err, ErrDanglingContract) {
		t.Fatalf("expected ErrDanglingContract, got %v", err)
	}
}

func TestCreate_RejectsArchivedContract(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	archived := "archived"
	if _, err := f.docs.Update(f.ctx, f.contractDoc.ID, document.UpdateFields{
		Status: &archived,
	}, "test", f.now, nil); err != nil {
		t.Fatalf("archive contract: %v", err)
	}
	_, err := f.contracts.Create(f.ctx, ContractInstance{
		StoryID:      f.parentStory.ID,
		ContractID:   f.contractDoc.ID,
		ContractName: "test_contract",
	}, f.now)
	if !errors.Is(err, ErrDanglingContract) {
		t.Fatalf("expected ErrDanglingContract, got %v", err)
	}
}

func TestCreate_RejectsNonContractType(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	other, err := f.docs.Create(f.ctx, document.Document{
		ID:     "doc_principle",
		Type:   document.TypePrinciple,
		Scope:  document.ScopeSystem,
		Name:   "p1",
		Body:   "principle body",
		Status: document.StatusActive,
	}, f.now)
	if err != nil {
		t.Fatalf("seed principle: %v", err)
	}
	_, err = f.contracts.Create(f.ctx, ContractInstance{
		StoryID:      f.parentStory.ID,
		ContractID:   other.ID,
		ContractName: "wrong_type",
	}, f.now)
	if !errors.Is(err, ErrDanglingContract) {
		t.Fatalf("expected ErrDanglingContract, got %v", err)
	}
}

func TestCreate_RejectsForeignWorkspaceProjectContract(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	// Project-scope contract doc in a different workspace.
	projID := "proj_B"
	foreign, err := f.docs.Create(f.ctx, document.Document{
		ID:          "doc_foreign_contract",
		WorkspaceID: wsB,
		ProjectID:   &projID,
		Type:        document.TypeContract,
		Scope:       document.ScopeProject,
		Name:        "foreign",
		Body:        "body",
		Status:      document.StatusActive,
	}, f.now)
	if err != nil {
		t.Fatalf("seed foreign contract: %v", err)
	}
	_, err = f.contracts.Create(f.ctx, ContractInstance{
		StoryID:      f.parentStory.ID,
		ContractID:   foreign.ID,
		ContractName: "foreign",
	}, f.now)
	if !errors.Is(err, ErrDanglingContract) {
		t.Fatalf("expected ErrDanglingContract for cross-workspace contract doc, got %v", err)
	}
}

func TestCreate_RejectsMissingStory(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	_, err := f.contracts.Create(f.ctx, ContractInstance{
		StoryID:      "sty_ghost",
		ContractID:   f.contractDoc.ID,
		ContractName: "test_contract",
	}, f.now)
	if !errors.Is(err, ErrMissingStory) {
		t.Fatalf("expected ErrMissingStory, got %v", err)
	}
}

// seedCI is a helper: Create one CI on the fixture's parent story.
func seedCI(t *testing.T, f *fixture, seq int, name string) ContractInstance {
	t.Helper()
	ci, err := f.contracts.Create(f.ctx, ContractInstance{
		StoryID:      f.parentStory.ID,
		ContractID:   f.contractDoc.ID,
		ContractName: name,
		Sequence:     seq,
	}, f.now)
	if err != nil {
		t.Fatalf("seedCI: %v", err)
	}
	return ci
}

func TestUpdateStatus_Transitions(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ci := seedCI(t, f, 0, "plan")

	// ready → claimed (ok)
	after, err := f.contracts.UpdateStatus(f.ctx, ci.ID, StatusClaimed, "actor", f.now.Add(time.Second), nil)
	if err != nil {
		t.Fatalf("ready→claimed: %v", err)
	}
	if after.Status != StatusClaimed {
		t.Fatalf("got %q want %q", after.Status, StatusClaimed)
	}

	// claimed → claimed (self) rejected
	if _, err := f.contracts.UpdateStatus(f.ctx, ci.ID, StatusClaimed, "actor", f.now, nil); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("self-transition: expected ErrInvalidTransition, got %v", err)
	}

	// claimed → passed (ok)
	after, err = f.contracts.UpdateStatus(f.ctx, ci.ID, StatusPassed, "actor", f.now.Add(2*time.Second), nil)
	if err != nil {
		t.Fatalf("claimed→passed: %v", err)
	}
	if after.Status != StatusPassed {
		t.Fatalf("got %q want %q", after.Status, StatusPassed)
	}

	// passed → anything rejected
	for _, target := range []string{StatusClaimed, StatusFailed, StatusSkipped} {
		if _, err := f.contracts.UpdateStatus(f.ctx, ci.ID, target, "actor", f.now, nil); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("passed→%s: expected ErrInvalidTransition, got %v", target, err)
		}
	}
}

func TestUpdateStatus_ReadyToPassedRejected(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ci := seedCI(t, f, 0, "plan")
	if _, err := f.contracts.UpdateStatus(f.ctx, ci.ID, StatusPassed, "actor", f.now, nil); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("ready→passed: expected ErrInvalidTransition, got %v", err)
	}
}

func TestUpdateStatus_UnknownStatus(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ci := seedCI(t, f, 0, "plan")
	if _, err := f.contracts.UpdateStatus(f.ctx, ci.ID, "bogus", "actor", f.now, nil); err == nil {
		t.Fatalf("expected error for unknown status")
	}
}

func TestList_OrderedBySequence(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	seedCI(t, f, 2, "develop")
	seedCI(t, f, 0, "plan")
	seedCI(t, f, 1, "develop")

	out, err := f.contracts.List(f.ctx, f.parentStory.ID, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("count: %d", len(out))
	}
	for i, want := range []int{0, 1, 2} {
		if out[i].Sequence != want {
			t.Fatalf("sequence[%d]: got %d want %d", i, out[i].Sequence, want)
		}
	}
}

func TestList_WorkspaceIsolation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	seedCI(t, f, 0, "plan")

	// Deny-all.
	out, err := f.contracts.List(f.ctx, f.parentStory.ID, []string{})
	if err != nil {
		t.Fatalf("deny-all List: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("deny-all: expected 0, got %d", len(out))
	}

	// Foreign workspace.
	out, err = f.contracts.List(f.ctx, f.parentStory.ID, []string{wsB})
	if err != nil {
		t.Fatalf("foreign List: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("foreign: expected 0, got %d", len(out))
	}

	// Caller's workspace.
	out, err = f.contracts.List(f.ctx, f.parentStory.ID, []string{wsA})
	if err != nil {
		t.Fatalf("scoped List: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("scoped: expected 1, got %d", len(out))
	}
}

func TestGetByID_WorkspaceIsolation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ci := seedCI(t, f, 0, "plan")

	if _, err := f.contracts.GetByID(f.ctx, ci.ID, []string{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deny-all: expected ErrNotFound, got %v", err)
	}
	if _, err := f.contracts.GetByID(f.ctx, ci.ID, []string{wsB}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign workspace: expected ErrNotFound, got %v", err)
	}
	if _, err := f.contracts.GetByID(f.ctx, ci.ID, []string{wsA}); err != nil {
		t.Fatalf("caller workspace: %v", err)
	}
	if _, err := f.contracts.GetByID(f.ctx, ci.ID, nil); err != nil {
		t.Fatalf("nil memberships: %v", err)
	}
}

func TestUpdateLedgerRefs_PartialUpdate(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ci := seedCI(t, f, 0, "plan")

	planID := "ldg_plan"
	updated, err := f.contracts.UpdateLedgerRefs(f.ctx, ci.ID, &planID, nil, "actor", f.now.Add(time.Second), nil)
	if err != nil {
		t.Fatalf("UpdateLedgerRefs plan: %v", err)
	}
	if updated.PlanLedgerID != planID {
		t.Fatalf("plan ref: got %q want %q", updated.PlanLedgerID, planID)
	}
	if updated.CloseLedgerID != "" {
		t.Fatalf("close ref clobbered: %q", updated.CloseLedgerID)
	}

	closeID := "ldg_close"
	updated, err = f.contracts.UpdateLedgerRefs(f.ctx, ci.ID, nil, &closeID, "actor", f.now.Add(2*time.Second), nil)
	if err != nil {
		t.Fatalf("UpdateLedgerRefs close: %v", err)
	}
	if updated.PlanLedgerID != planID {
		t.Fatalf("plan ref cleared: %q", updated.PlanLedgerID)
	}
	if updated.CloseLedgerID != closeID {
		t.Fatalf("close ref: got %q want %q", updated.CloseLedgerID, closeID)
	}
}

func TestBackfillWorkspaceID(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ci := seedCI(t, f, 0, "plan")

	// Zero out workspace_id to simulate a legacy row.
	f.contracts.mu.Lock()
	raw := f.contracts.rows[ci.ID]
	raw.WorkspaceID = ""
	f.contracts.rows[ci.ID] = raw
	f.contracts.mu.Unlock()

	n, err := f.contracts.BackfillWorkspaceID(f.ctx, "proj_A", wsA, f.now.Add(time.Second))
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if n != 1 {
		t.Fatalf("backfill n: got %d want 1", n)
	}
	got, err := f.contracts.GetByID(f.ctx, ci.ID, []string{wsA})
	if err != nil {
		t.Fatalf("get after backfill: %v", err)
	}
	if got.WorkspaceID != wsA {
		t.Fatalf("backfilled workspace: got %q want %q", got.WorkspaceID, wsA)
	}

	// Second invocation is a no-op.
	n, err = f.contracts.BackfillWorkspaceID(f.ctx, "proj_A", wsA, f.now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	if n != 0 {
		t.Fatalf("second backfill should be no-op, got %d", n)
	}
}

func TestNewMemoryStore_NilDepsPanic(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Fatalf("expected panic on nil docs")
		}
	}()
	_ = NewMemoryStore(nil, nil)
}
