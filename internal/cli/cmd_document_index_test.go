package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// TestBuildDocumentIndex confirms the discovery index projects identity +
// residency + headline from the document_list rows (bodies never fetched): a
// principles:always row is flagged and sorts first, a plain row is unflagged,
// and the headline round-trips from the listing into the entry.
func TestBuildDocumentIndex(t *testing.T) {
	const listResp = `{"items":[
		{"name":"broken-windows","scope":"project","tags":["principles:project","principles:always"],"headline":"fix small breakage now"},
		{"name":"glossary","scope":"project","tags":["area:docs"],"headline":"terms and definitions"},
		{"name":"agent-goals","scope":"project","tags":["principles:always"],"headline":"drive story to terminal state"}
	]}`
	dispatch := func(_ context.Context, name string, _ json.RawMessage) (json.RawMessage, error) {
		if name == "document_list" {
			return json.RawMessage(listResp), nil
		}
		return nil, fmt.Errorf("unexpected verb %q (index must not load bodies)", name)
	}

	index, truncated, err := buildDocumentIndex(context.Background(), dispatch, "project", "wksp_X", "proj_Y")
	if err != nil {
		t.Fatalf("buildDocumentIndex: %v", err)
	}
	if truncated {
		t.Fatalf("did not expect truncation for 3 rows")
	}
	if len(index) != 3 {
		t.Fatalf("want 3 entries, got %d: %+v", len(index), index)
	}

	// Resident (always) rows sort first, then by name: agent-goals,
	// broken-windows (both always), then glossary (not always).
	if index[0].Name != "agent-goals" || !index[0].Always {
		t.Errorf("entry[0] = %+v; want agent-goals flagged always and sorted first", index[0])
	}
	if index[1].Name != "broken-windows" || !index[1].Always {
		t.Errorf("entry[1] = %+v; want broken-windows flagged always", index[1])
	}
	if index[2].Name != "glossary" || index[2].Always {
		t.Errorf("entry[2] = %+v; want glossary unflagged and sorted last", index[2])
	}
	// Headline carries through unchanged.
	if index[0].Headline != "drive story to terminal state" {
		t.Errorf("headline did not round-trip: got %q", index[0].Headline)
	}
}

// TestBuildDocumentIndexTruncates asserts the listing is capped and the overflow
// is surfaced (never silently dropped): more rows than the cap → exactly cap
// entries returned and truncated=true (epic:always-context AC4).
func TestBuildDocumentIndexTruncates(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"items":[`)
	for i := 0; i < documentIndexCap+5; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"name":%s,"scope":"project","tags":[],"headline":""}`,
			strconv.Quote(fmt.Sprintf("doc-%04d", i)))
	}
	b.WriteString(`]}`)
	listResp := b.String()

	dispatch := func(_ context.Context, name string, _ json.RawMessage) (json.RawMessage, error) {
		if name == "document_list" {
			return json.RawMessage(listResp), nil
		}
		return nil, fmt.Errorf("unexpected verb %q", name)
	}

	index, truncated, err := buildDocumentIndex(context.Background(), dispatch, "project", "", "proj_Y")
	if err != nil {
		t.Fatalf("buildDocumentIndex: %v", err)
	}
	if !truncated {
		t.Fatalf("want truncated=true when rows exceed the cap")
	}
	if len(index) != documentIndexCap {
		t.Fatalf("want %d capped entries, got %d", documentIndexCap, len(index))
	}
}
