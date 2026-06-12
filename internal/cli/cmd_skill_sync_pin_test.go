package cli

import (
	"strings"
	"testing"
)

// TestMergeByPrecedence_LibraryLowest pins AC3 of sty_56855694: a pinned
// library skill loses a name collision to every repo-owned scope — the
// library set is passed first (lowest precedence).
func TestMergeByPrecedence_LibraryLowest(t *testing.T) {
	lib := []substrateSkill{{Name: "sec-scan", Body: "library-body", Publisher: "proj_pub"}}
	pj := []substrateSkill{{Name: "sec-scan", Body: "project-body"}}
	merged := mergeByPrecedence(lib, nil, nil, pj)
	if len(merged) != 1 {
		t.Fatalf("merged len=%d want 1", len(merged))
	}
	if merged[0].Body != "project-body" || merged[0].Publisher != "" {
		t.Fatalf("project did not win the collision: %+v", merged[0])
	}
	// A pin with no collision survives the merge with its publisher intact.
	merged = mergeByPrecedence(lib, nil, nil, nil)
	if merged[0].Publisher != "proj_pub" {
		t.Fatalf("publisher lost in merge: %+v", merged[0])
	}
}

// TestMaterialise_PublisherStamp pins AC2: a library-pinned skill's
// materialised stamp records the publisher alongside version + document
// identity, and a scope-owned skill's stamp stays publisher-free.
func TestMaterialise_PublisherStamp(t *testing.T) {
	pinned := substrateSkill{
		Name: "sec-scan", DocumentID: "doc_lib00001", Version: 3,
		Body:      "---\nname: sec-scan\ndescription: d\n---\n\n# S\n",
		Publisher: "proj_pub",
	}
	out := materialise(pinned)
	for _, want := range []string{`"document_id":"doc_lib00001"`, `"version":3`, `"publisher":"proj_pub"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("stamp missing %q:\n%s", want, out)
		}
	}
	owned := pinned
	owned.Publisher = ""
	if strings.Contains(materialise(owned), `"publisher"`) {
		t.Fatalf("scope-owned stamp gained a publisher field:\n%s", materialise(owned))
	}
}
