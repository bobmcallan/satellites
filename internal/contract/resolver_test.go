package contract

import (
	"reflect"
	"testing"
)

// TestMergeSlots_SystemOnly is the baseline: when only the system tier
// supplies slots, the merged spec preserves them with Source=system.
func TestMergeSlots_SystemOnly(t *testing.T) {
	t.Parallel()
	system := []Slot{
		{ContractName: "preplan", Required: true, MinCount: 1, MaxCount: 1},
		{ContractName: "plan", Required: true, MinCount: 1, MaxCount: 1},
	}
	got := MergeSlots(LayerSlots{Source: SourceSystem, Slots: system})
	want := []Slot{
		{ContractName: "preplan", Required: true, MinCount: 1, MaxCount: 1, Source: SourceSystem},
		{ContractName: "plan", Required: true, MinCount: 1, MaxCount: 1, Source: SourceSystem},
	}
	if !reflect.DeepEqual(got.Slots, want) {
		t.Fatalf("merged slots: got %+v want %+v", got.Slots, want)
	}
}

// TestMergeSlots_ProjectAddsSlot covers the worked example from the
// design doc: system supplies the 6-slot default; project ships one
// additional `compliance_review` slot. The merged list ends with the
// project addition tagged Source=project.
func TestMergeSlots_ProjectAddsSlot(t *testing.T) {
	t.Parallel()
	system := []Slot{
		{ContractName: "preplan", Required: true, MinCount: 1, MaxCount: 1},
		{ContractName: "plan", Required: true, MinCount: 1, MaxCount: 1},
	}
	project := []Slot{
		{ContractName: "compliance_review", Required: true, MinCount: 1, MaxCount: 1},
	}
	got := MergeSlots(
		LayerSlots{Source: SourceSystem, Slots: system},
		LayerSlots{Source: SourceProject, Slots: project},
	)
	want := []Slot{
		{ContractName: "preplan", Required: true, MinCount: 1, MaxCount: 1, Source: SourceSystem},
		{ContractName: "plan", Required: true, MinCount: 1, MaxCount: 1, Source: SourceSystem},
		{ContractName: "compliance_review", Required: true, MinCount: 1, MaxCount: 1, Source: SourceProject},
	}
	if !reflect.DeepEqual(got.Slots, want) {
		t.Fatalf("merged slots: got %+v want %+v", got.Slots, want)
	}
}

// TestMergeSlots_UserAddsSlot exercises the user-tier override path —
// a per-user workflow markdown adding an extra slot the merged spec
// must include.
func TestMergeSlots_UserAddsSlot(t *testing.T) {
	t.Parallel()
	system := []Slot{
		{ContractName: "preplan", Required: true, MinCount: 1, MaxCount: 1},
	}
	user := []Slot{
		{ContractName: "personal_review", Required: true, MinCount: 1, MaxCount: 1},
	}
	got := MergeSlots(
		LayerSlots{Source: SourceSystem, Slots: system},
		LayerSlots{Source: SourceUser, Slots: user},
	)
	want := []Slot{
		{ContractName: "preplan", Required: true, MinCount: 1, MaxCount: 1, Source: SourceSystem},
		{ContractName: "personal_review", Required: true, MinCount: 1, MaxCount: 1, Source: SourceUser},
	}
	if !reflect.DeepEqual(got.Slots, want) {
		t.Fatalf("merged slots: got %+v want %+v", got.Slots, want)
	}
}

// TestMergeSlots_ConflictingMinMax verifies the stricter-constraint
// rule: max(min_count) and min(non-zero max_count). Source becomes
// SourceMerged when ≥2 layers contribute to the same slot.
func TestMergeSlots_ConflictingMinMax(t *testing.T) {
	t.Parallel()
	system := []Slot{
		{ContractName: "develop", Required: true, MinCount: 1, MaxCount: 10},
	}
	project := []Slot{
		{ContractName: "develop", Required: true, MinCount: 2, MaxCount: 5},
	}
	got := MergeSlots(
		LayerSlots{Source: SourceSystem, Slots: system},
		LayerSlots{Source: SourceProject, Slots: project},
	)
	want := []Slot{
		{ContractName: "develop", Required: true, MinCount: 2, MaxCount: 5, Source: SourceMerged},
	}
	if !reflect.DeepEqual(got.Slots, want) {
		t.Fatalf("merged slots: got %+v want %+v", got.Slots, want)
	}
}

// TestMergeSlots_RequiredUpgrade covers the asymmetric upgrade rule:
// required:false at a parent is upgradable to required:true at a
// child but never downgradable.
func TestMergeSlots_RequiredUpgrade(t *testing.T) {
	t.Parallel()
	system := []Slot{
		{ContractName: "audit", Required: false, MinCount: 0, MaxCount: 1},
	}
	project := []Slot{
		{ContractName: "audit", Required: true, MinCount: 1, MaxCount: 1},
	}
	got := MergeSlots(
		LayerSlots{Source: SourceSystem, Slots: system},
		LayerSlots{Source: SourceProject, Slots: project},
	)
	if len(got.Slots) != 1 {
		t.Fatalf("merged slots count: got %d want 1", len(got.Slots))
	}
	if !got.Slots[0].Required {
		t.Fatalf("required: got false want true after merge")
	}
	if got.Slots[0].Source != SourceMerged {
		t.Fatalf("source: got %q want %q", got.Slots[0].Source, SourceMerged)
	}

	// Reverse direction: child required:false against system required:true
	// must NOT downgrade.
	systemReq := []Slot{
		{ContractName: "audit", Required: true, MinCount: 1, MaxCount: 1},
	}
	projectOpt := []Slot{
		{ContractName: "audit", Required: false, MinCount: 0, MaxCount: 1},
	}
	gotDown := MergeSlots(
		LayerSlots{Source: SourceSystem, Slots: systemReq},
		LayerSlots{Source: SourceProject, Slots: projectOpt},
	)
	if !gotDown.Slots[0].Required {
		t.Fatalf("required: got false want true (no downgrade allowed)")
	}
}

// TestSourceFor returns the source for the matching slot.
func TestSourceFor(t *testing.T) {
	t.Parallel()
	spec := WorkflowSpec{Slots: []Slot{
		{ContractName: "preplan", Source: SourceSystem},
		{ContractName: "compliance_review", Source: SourceProject},
	}}
	if got := SourceFor(spec, "compliance_review"); got != SourceProject {
		t.Fatalf("got %q want %q", got, SourceProject)
	}
	if got := SourceFor(spec, "missing"); got != "" {
		t.Fatalf("missing slot source: got %q want empty", got)
	}
}
