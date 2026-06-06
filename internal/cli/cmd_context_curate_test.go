package cli

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/verb"
)

func TestWithoutTag(t *testing.T) {
	got := withoutTag([]string{"principles:global", verb.PrincipleTagAlways, "x"}, verb.PrincipleTagAlways)
	if strings.Join(got, ",") != "principles:global,x" {
		t.Fatalf("withoutTag = %v", got)
	}
	// removing an absent tag is a no-op (copy)
	got2 := withoutTag([]string{"a", "b"}, verb.PrincipleTagAlways)
	if strings.Join(got2, ",") != "a,b" {
		t.Fatalf("withoutTag absent = %v", got2)
	}
	// removes all occurrences
	got3 := withoutTag([]string{"t", "t", "k"}, "t")
	if strings.Join(got3, ",") != "k" {
		t.Fatalf("withoutTag dup = %v", got3)
	}
}

func TestWithTag(t *testing.T) {
	got := withTag([]string{"principles:global"}, verb.PrincipleTagAlways)
	if len(got) != 2 || got[1] != verb.PrincipleTagAlways {
		t.Fatalf("withTag append = %v", got)
	}
	// idempotent: already present -> no duplicate
	got2 := withTag([]string{"principles:global", verb.PrincipleTagAlways}, verb.PrincipleTagAlways)
	if len(got2) != 2 {
		t.Fatalf("withTag should not duplicate, got %v", got2)
	}
}

// drop→restore round-trips the tag set (modulo ordering).
func TestCurateTagToggleRoundTrip(t *testing.T) {
	base := []string{"principles:global"}
	dropped := withoutTag(withTag(base, verb.PrincipleTagAlways), verb.PrincipleTagAlways)
	if strings.Join(dropped, ",") != "principles:global" {
		t.Fatalf("add then remove should return to base, got %v", dropped)
	}
}
