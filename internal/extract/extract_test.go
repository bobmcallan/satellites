package extract

import "testing"

// TestText_PlainTextAndClassification covers passthrough + the type guards.
func TestText_PlainTextAndClassification(t *testing.T) {
	if got, ok := Text("notes.md", "text/markdown", []byte("# hi\nbody")); !ok || got != "# hi\nbody" {
		t.Errorf("markdown passthrough: got %q ok=%v", got, ok)
	}
	if got, ok := Text("a.txt", "", []byte("plain")); !ok || got != "plain" {
		t.Errorf("txt by extension: got %q ok=%v", got, ok)
	}
	if _, ok := Text("empty.txt", "text/plain", []byte("   \n  ")); ok {
		t.Error("whitespace-only text should be ok=false")
	}
	if _, ok := Text("photo.png", "image/png", []byte{0x89, 0x50}); ok {
		t.Error("unsupported image type should be ok=false (OCR deferred)")
	}
	if _, ok := Text("data.bin", "application/octet-stream", []byte{1, 2, 3}); ok {
		t.Error("unknown binary should be ok=false")
	}
}

// TestText_MalformedPDF_NoPanic: a PDF-typed but garbage payload must return
// ok=false without panicking (the recover in pdfText).
func TestText_MalformedPDF_NoPanic(t *testing.T) {
	if _, ok := Text("broken.pdf", "application/pdf", []byte("not really a pdf")); ok {
		t.Error("malformed PDF should extract nothing (ok=false)")
	}
}

// TestSupportedAndIsImage pins the accepted-upload set (sty_49a3762e): PDFs,
// plain text/markdown, AND images are Supported; images are classified by IsImage
// (by content-type prefix or extension) so ingest stores them as metadata-only
// documents. Text extraction for an image stays ok=false (OCR deferred).
func TestSupportedAndIsImage(t *testing.T) {
	supported := []struct{ fn, ct string }{
		{"a.pdf", "application/pdf"},
		{"a.md", "text/markdown"},
		{"photo.png", "image/png"},
		{"photo.JPG", ""},        // by extension, case-insensitive
		{"x", "image/webp"},      // by content-type alone
		{"diagram.gif", ""},
	}
	for _, c := range supported {
		if !Supported(c.fn, c.ct) {
			t.Errorf("Supported(%q,%q) = false, want true", c.fn, c.ct)
		}
	}

	images := []struct {
		fn, ct string
		want   bool
	}{
		{"photo.png", "", true},
		{"p.jpg", "", true},
		{"p.jpeg", "", true},
		{"p.gif", "", true},
		{"p.webp", "", true},
		{"x", "image/svg+xml", true},
		{"notes.md", "text/markdown", false},
		{"a.pdf", "application/pdf", false},
		{"data.bin", "application/octet-stream", false},
	}
	for _, c := range images {
		if got := IsImage(c.fn, c.ct); got != c.want {
			t.Errorf("IsImage(%q,%q) = %v, want %v", c.fn, c.ct, got, c.want)
		}
	}

	// A truly-unsupported binary is still rejected.
	if Supported("data.bin", "application/octet-stream") {
		t.Error("Supported(data.bin) = true, want false")
	}
}
