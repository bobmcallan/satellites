package cli

import (
	"reflect"
	"testing"
)

// TestMergeCloseOutTags_PreservesAndReplaces: a close-out tag patch keeps the
// story's other tags and replaces an existing estimate key in place (no dupe).
func TestMergeCloseOutTags_PreservesAndReplaces(t *testing.T) {
	existing := []string{"area:workflow", "workflow:satellites-workflow", "estimate-minutes:10"}
	got := mergeCloseOutTags(existing, map[string]string{
		tagEstimateMinutes: "30",
		tagEstimateTokens:  "40000",
	})
	want := []string{"area:workflow", "workflow:satellites-workflow", "estimate-minutes:30", "estimate-tokens:40000"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("merge = %v want %v", got, want)
	}
}

func TestDurationMinutes(t *testing.T) {
	cases := map[string]int{"30m": 30, "1h30m": 90, "90s": 2, "0s": 0}
	for in, want := range cases {
		got, err := durationMinutes(in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if got != want {
			t.Fatalf("%s = %d want %d", in, got, want)
		}
	}
	if _, err := durationMinutes("notaduration"); err == nil {
		t.Fatalf("expected a parse error for a non-duration")
	}
}

func TestSanitizeTagValue(t *testing.T) {
	if got := sanitizeTagValue("  2 files,\n gate  loop  "); got != "2 files, gate loop" {
		t.Fatalf("sanitize = %q want %q", got, "2 files, gate loop")
	}
	if got := sanitizeTagValue("   "); got != "" {
		t.Fatalf("sanitize blank = %q want empty", got)
	}
}
