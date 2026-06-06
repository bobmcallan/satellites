package cli

import (
	"encoding/json"
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

// the ride-along list shape — the --json payload + the trimmable subtotal.
func TestCurateListShape(t *testing.T) {
	ps := []ridealongPrincipleInfo{
		{Name: "a", ID: "doc_a", Scope: "global", Bytes: 100},
		{Name: "b", ID: "doc_b", Scope: "project", Bytes: 50},
	}
	if got := trimmableBytes(ps); got != 150 {
		t.Fatalf("trimmableBytes = %d, want 150", got)
	}
	j := buildCurateJSON("sty_x", ps, 7000)
	if j["story_id"] != "sty_x" {
		t.Errorf("story_id = %v", j["story_id"])
	}
	if j["always_on_bytes"].(int) != 7000 {
		t.Errorf("always_on_bytes = %v", j["always_on_bytes"])
	}
	if j["trimmable_bytes"].(int) != 150 {
		t.Errorf("trimmable_bytes = %v", j["trimmable_bytes"])
	}
	if rl := j["ridealong"].([]ridealongPrincipleInfo); len(rl) != 2 || rl[0].Name != "a" {
		t.Errorf("ridealong = %v", j["ridealong"])
	}
	if fc := j["fixed_components"].([]string); len(fc) != 3 {
		t.Errorf("fixed_components = %v", fc)
	}
	// round-trips through JSON (per-principle rows survive)
	raw, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	var back struct {
		Ridealong []ridealongPrincipleInfo `json:"ridealong"`
		Trimmable int                      `json:"trimmable_bytes"`
	}
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Ridealong) != 2 || back.Ridealong[1].Bytes != 50 || back.Trimmable != 150 {
		t.Fatalf("json round-trip mismatch: %+v", back)
	}
}

// dropping a ride-along principle reduces the always-on total by exactly its
// bytes — the measure-shrink curate's --drop performs, via the real total owner.
func TestDropReducesAlwaysOnMeasure(t *testing.T) {
	const principleBytes = 191
	fixed := []contextComponent{
		{Name: "load-context (MCP instructions)", Partition: partAlwaysOn, Bytes: 1839},
		{Name: "skills index", Partition: partAlwaysOn, Bytes: 5403},
	}
	ride := []ridealongPrincipleInfo{{Name: "p", ID: "doc_p", Scope: "project", Bytes: principleBytes}}
	withPrinciple := append(append([]contextComponent{}, fixed...),
		contextComponent{Name: "principles ride-along (sidecar)", Partition: partAlwaysOn, Bytes: trimmableBytes(ride)})

	before := sumPartitionTotals(withPrinciple)[partAlwaysOn]
	after := sumPartitionTotals(fixed)[partAlwaysOn] // ride-along dropped → 0
	if !(before > after) {
		t.Fatalf("drop should reduce always-on: before=%d after=%d", before, after)
	}
	if before-after != principleBytes {
		t.Fatalf("drop should reduce always-on by the principle's %d bytes, got %d", principleBytes, before-after)
	}
}
