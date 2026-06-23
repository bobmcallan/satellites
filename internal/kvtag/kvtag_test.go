package kvtag

import (
	"reflect"
	"testing"
)

func TestValue(t *testing.T) {
	tags := []string{"epic:phases-task-outputs", "type:diagram", "phase:discovery"}
	if got := Value(tags, "type"); got != "diagram" {
		t.Errorf("Value type = %q, want diagram", got)
	}
	if got := Value(tags, "phase"); got != "discovery" {
		t.Errorf("Value phase = %q, want discovery", got)
	}
	if got := Value(tags, "missing"); got != "" {
		t.Errorf("Value missing = %q, want empty", got)
	}
	// First-wins for a multi-valued key.
	if got := Value([]string{"area:cli", "area:bootstrap"}, "area"); got != "cli" {
		t.Errorf("Value area = %q, want cli (first)", got)
	}
}

func TestValueRejectsNonKV(t *testing.T) {
	// A leading colon or an empty value does not parse as KV.
	for _, tag := range []string{":nope", "type:", "plainword"} {
		if Has([]string{tag}, "type") && tag != "type:value" {
			// only assert the malformed ones
		}
	}
	if Has([]string{"type:"}, "type") {
		t.Error("empty-value tag type: should not parse as KV")
	}
	if Has([]string{":discovery"}, "") {
		t.Error("leading-colon tag should not parse as KV")
	}
}

func TestHas(t *testing.T) {
	tags := []string{"type:diagram"}
	if !Has(tags, "type") {
		t.Error("Has type = false, want true")
	}
	if Has(tags, "phase") {
		t.Error("Has phase = true, want false")
	}
}

func TestSetAddsWhenAbsent(t *testing.T) {
	got := Set([]string{"epic:x"}, "phase", "discovery")
	want := []string{"epic:x", "phase:discovery"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Set add = %v, want %v", got, want)
	}
}

func TestSetReplacesExisting(t *testing.T) {
	got := Set([]string{"type:doc", "phase:discovery", "area:cli"}, "phase", "build")
	want := []string{"type:doc", "area:cli", "phase:build"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Set replace = %v, want %v", got, want)
	}
}

func TestSetCollapsesDuplicateKey(t *testing.T) {
	// Two existing phase: tags both removed, single new one appended.
	got := Set([]string{"phase:discovery", "phase:build"}, "phase", "ship")
	want := []string{"phase:ship"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Set collapse = %v, want %v", got, want)
	}
}

func TestSetDoesNotMutateInput(t *testing.T) {
	in := []string{"phase:discovery"}
	_ = Set(in, "phase", "build")
	if in[0] != "phase:discovery" {
		t.Errorf("Set mutated input: %v", in)
	}
}

func TestRemove(t *testing.T) {
	got := Remove([]string{"type:doc", "phase:discovery", "area:cli"}, "type")
	want := []string{"phase:discovery", "area:cli"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Remove = %v, want %v", got, want)
	}
	// Removing an absent key is a no-op copy.
	if got := Remove([]string{"phase:discovery"}, "type"); !reflect.DeepEqual(got, []string{"phase:discovery"}) {
		t.Errorf("Remove absent = %v", got)
	}
}

func TestNormalizeCollapsesSingleValuedLastWins(t *testing.T) {
	got := Normalize([]string{"type:a", "phase:discovery", "type:b", "phase:build"})
	want := []string{"type:b", "phase:build"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Normalize = %v, want %v", got, want)
	}
}

func TestNormalizeKeepsMultiValuedKeys(t *testing.T) {
	// area: is intentionally multi-valued — both survive.
	in := []string{"area:cli", "area:bootstrap", "phase:discovery"}
	got := Normalize(in)
	if !reflect.DeepEqual(got, in) {
		t.Errorf("Normalize dropped multi-valued tags: %v, want %v", got, in)
	}
}

func TestNormalizePreservesOrderAndNonKV(t *testing.T) {
	got := Normalize([]string{"epic:x", "type:a", "plainword", "type:b"})
	want := []string{"epic:x", "plainword", "type:b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Normalize = %v, want %v", got, want)
	}
}

func TestNormalizeNilAndEmpty(t *testing.T) {
	if got := Normalize(nil); got != nil {
		t.Errorf("Normalize(nil) = %v, want nil", got)
	}
	if got := Normalize([]string{}); len(got) != 0 {
		t.Errorf("Normalize(empty) = %v, want empty", got)
	}
}
