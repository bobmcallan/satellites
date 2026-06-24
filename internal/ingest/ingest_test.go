package ingest

import "testing"

// TestDocName pins sty_c62e9b63: a portal upload names its document from the
// uploaded filename's stem (extension stripped), not "attachment-<blobID>",
// falling back to the blob-id name only when the filename yields nothing usable.
func TestDocName(t *testing.T) {
	const blobID = "blob_abc123"
	cases := []struct {
		name     string
		filename string
		want     string
	}{
		{"markdown stem", "architecture-as-built-2014.md", "architecture-as-built-2014"},
		{"manifest", "00-MANIFEST.md", "00-MANIFEST"},
		{"multi-dot keeps inner dots", "report.v2.final.md", "report.v2.final"},
		{"no extension", "NOTES", "NOTES"},
		{"path is stripped to base", "docs/sub/spec.txt", "spec"},
		{"empty filename falls back", "", "attachment-" + blobID},
		{"dotfile falls back", ".gitignore", "attachment-" + blobID},
		{"separator falls back", "/", "attachment-" + blobID},
		{"whitespace trimmed then fallback", "   ", "attachment-" + blobID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := docName(tc.filename, blobID); got != tc.want {
				t.Errorf("docName(%q) = %q, want %q", tc.filename, got, tc.want)
			}
		})
	}
}
