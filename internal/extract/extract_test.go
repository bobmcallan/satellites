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
