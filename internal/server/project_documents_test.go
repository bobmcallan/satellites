package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bobmcallan/satellites/internal/auth"
)

// TestFilterDocs pins the Documents panel chip filter (this story AC2): type:,
// phase:, tags:, and free text select the right rows via the shared panel grammar.
func TestFilterDocs(t *testing.T) {
	rows := []docRow{
		{ID: "doc_1", Name: "DDD domains", Tags: []string{"type:document", "phase:discovery"}},
		{ID: "doc_2", Name: "Diagram one", Tags: []string{"type:diagram", "phase:build"}},
		{ID: "doc_3", Name: "report card", Tags: []string{"report"}},
	}
	cases := []struct {
		q    string
		want []string
	}{
		{"", []string{"doc_1", "doc_2", "doc_3"}},
		{"type:document", []string{"doc_1"}},
		{"phase:build", []string{"doc_2"}},
		{"tags:report", []string{"doc_3"}},
		{"ddd", []string{"doc_1"}},
		{"phase:discovery type:document", []string{"doc_1"}},
	}
	for _, c := range cases {
		got := filterDocs(rows, parseStoryQuery(c.q))
		var ids []string
		for _, r := range got {
			ids = append(ids, r.ID)
		}
		if !equalStrs(ids, c.want) {
			t.Errorf("filterDocs(%q) = %v, want %v", c.q, ids, c.want)
		}
	}
}

// TestDocExpandable pins AC3: known text documents expand inline; clearly-binary
// rows (by KV type or filename extension) do not.
func TestDocExpandable(t *testing.T) {
	cases := []struct {
		name, kvType string
		want         bool
	}{
		{"DDD domains", "document", true},
		{"attachment-abc", "", true},
		{"notes.md", "document", true},
		{"design.mdx", "", true},
		{"scan.pdf", "", false},
		{"photo.png", "", false},
		{"chart", "image", false},
		{"bundle.zip", "", false},
	}
	for _, c := range cases {
		if got := docExpandable(c.name, c.kvType); got != c.want {
			t.Errorf("docExpandable(%q, %q) = %v, want %v", c.name, c.kvType, got, c.want)
		}
	}
}

// TestProjectDocumentUpload_Unauthenticated pins AC5: with no session the upload
// is refused before any parsing or store touch.
func TestProjectDocumentUpload_Unauthenticated(t *testing.T) {
	h := projectDocumentUploadHandler(Config{Sessions: auth.NewSessions([]byte("test-secret"))})
	req := httptest.NewRequest(http.MethodPost, "/projects/proj_x/documents", nil)
	req.SetPathValue("id", "proj_x")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for an unauthenticated upload", rr.Code)
	}
}

func equalStrs(a, b []string) bool {
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
