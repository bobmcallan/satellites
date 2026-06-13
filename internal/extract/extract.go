// Package extract turns an uploaded binary into readable text for the
// document-collection corpus (sty_52c2393f): PDFs via a pure-Go reader,
// plain-text/markdown by passthrough. Image OCR is deferred — it needs an
// external engine (tesseract) in the server image. Extraction is best-effort:
// an unsupported or unreadable input yields ok=false and the caller stores the
// blob with no text document.
package extract

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/ledongthuc/pdf"
)

// Text extracts readable text from content given its filename and content type.
// Returns the text with ok=true when extraction produced non-empty output;
// ok=false for an unsupported type or an empty/failed/panicking extraction (the
// blob is still retained by the caller either way).
func Text(filename, contentType string, content []byte) (string, bool) {
	switch {
	case isPDF(filename, contentType):
		t, err := pdfText(content)
		if err != nil || strings.TrimSpace(t) == "" {
			return "", false
		}
		return t, true
	case isPlainText(filename, contentType):
		if strings.TrimSpace(string(content)) == "" {
			return "", false
		}
		return string(content), true
	default:
		return "", false
	}
}

// Supported reports whether a filename/content-type is an extractable document
// type (PDF or plain text/markdown) — the same set Text can turn into corpus
// text. A caller can pre-check this to reject an unsupported upload before
// storing a blob that would yield no document (sty_5d97b972).
func Supported(filename, contentType string) bool {
	return isPDF(filename, contentType) || isPlainText(filename, contentType)
}

func isPDF(filename, contentType string) bool {
	return strings.Contains(strings.ToLower(contentType), "pdf") ||
		strings.HasSuffix(strings.ToLower(filename), ".pdf")
}

func isPlainText(filename, contentType string) bool {
	if strings.HasPrefix(strings.ToLower(contentType), "text/") {
		return true
	}
	fn := strings.ToLower(filename)
	for _, ext := range []string{".txt", ".md", ".markdown", ".csv", ".log"} {
		if strings.HasSuffix(fn, ext) {
			return true
		}
	}
	return false
}

// pdfText reads all page text from a PDF in memory. The ledongthuc reader can
// panic on malformed input, so a recover converts that into an error — a bad
// upload must never crash the server.
func pdfText(content []byte) (text string, err error) {
	defer func() {
		if r := recover(); r != nil {
			text, err = "", fmt.Errorf("pdf extract panicked: %v", r)
		}
	}()
	r, err := pdf.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return "", err
	}
	rc, err := r.GetPlainText()
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, rc); err != nil {
		return "", err
	}
	return buf.String(), nil
}
