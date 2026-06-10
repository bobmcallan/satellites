package verb

import (
	"testing"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/document"
)

// TestMCPReadForbiddenDoc pins the read-gate decision: over MCP, skills and
// principle-tagged documents are refused; plain reference documents and stories
// pass; any non-MCP transport passes everything (epic:authoring-as-config).
func TestMCPReadForbiddenDoc(t *testing.T) {
	cases := []struct {
		name    string
		tr      auth.Transport
		docType string
		tags    []string
		want    bool
	}{
		{"MCP skill → forbidden", auth.TransportMCP, document.TypeSkill, nil, true},
		{"MCP principle:always → forbidden", auth.TransportMCP, document.TypeDocument, []string{"principles:always"}, true},
		{"MCP principle:project → forbidden", auth.TransportMCP, document.TypeDocument, []string{"area:x", "principles:project"}, true},
		{"MCP reference doc → allowed", auth.TransportMCP, document.TypeDocument, []string{"area:docs"}, false},
		{"MCP story → allowed", auth.TransportMCP, document.TypeStory, nil, false},
		{"non-MCP skill → allowed", "", document.TypeSkill, nil, false},
		{"non-MCP principle → allowed", "", document.TypeDocument, []string{"principles:always"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := mcpReadForbiddenDoc(c.tr, c.docType, c.tags); got != c.want {
				t.Errorf("mcpReadForbiddenDoc(%q,%q,%v) = %v, want %v", c.tr, c.docType, c.tags, got, c.want)
			}
		})
	}
}

// TestFilterMCPReadable confirms a mixed listing over MCP drops skills +
// principles while keeping reference docs and stories, and that a non-MCP
// transport returns the list untouched.
func TestFilterMCPReadable(t *testing.T) {
	items := []document.Document{
		{Name: "skill-a", Type: document.TypeSkill},
		{Name: "ref-doc", Type: document.TypeDocument, Tags: []string{"area:docs"}},
		{Name: "agent-goals", Type: document.TypeDocument, Tags: []string{"principles:always"}},
		{Name: "sty-x", Type: document.TypeStory},
	}

	got := filterMCPReadable(auth.TransportMCP, items)
	gotNames := map[string]bool{}
	for _, d := range got {
		gotNames[d.Name] = true
	}
	if len(got) != 2 || !gotNames["ref-doc"] || !gotNames["sty-x"] {
		t.Errorf("MCP filter should keep only ref-doc + sty-x, got %v", gotNames)
	}

	// Non-MCP transport: unchanged.
	if got := filterMCPReadable("", items); len(got) != len(items) {
		t.Errorf("non-MCP should pass all %d items, got %d", len(items), len(got))
	}
}

// TestMCPForbidsSkillList pins the explicit type=skill list/count refusal over
// MCP (vs silently returning empty), and that other types / non-MCP pass.
func TestMCPForbidsSkillList(t *testing.T) {
	if !mcpForbidsSkillList(auth.TransportMCP, "skill") {
		t.Error("MCP type=skill list must be refused")
	}
	if mcpForbidsSkillList(auth.TransportMCP, "document") {
		t.Error("MCP type=document list must NOT be refused (reference docs)")
	}
	if mcpForbidsSkillList("", "skill") {
		t.Error("non-MCP type=skill list must pass")
	}
}
