package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/cliconfig"
)

const codeSampleGo = `package sample

func Widget(n int) int { return n * 2 }

type Gadget struct{ ID string }
`

// writeCodeRepo lays down a minimal repo and returns its root + the index.db path.
func writeCodeRepo(t *testing.T) (root, dbPath string) {
	t.Helper()
	root = t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte(codeSampleGo), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, cliconfig.Config{}.ResolveIndexDB(root)
}

func TestRunCodeIndexThenSearchAndSymbol(t *testing.T) {
	root, dbPath := writeCodeRepo(t)

	var idxOut bytes.Buffer
	if err := runCodeIndex(&idxOut, root, dbPath, false); err != nil {
		t.Fatalf("index: %v", err)
	}
	if !strings.Contains(idxOut.String(), "indexed") {
		t.Errorf("index output unexpected: %q", idxOut.String())
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("index.db not written: %v", err)
	}

	var searchOut bytes.Buffer
	if err := runCodeSearch(&searchOut, dbPath, "Widget", 25, false); err != nil {
		t.Fatalf("search: %v", err)
	}
	got := searchOut.String()
	if !strings.Contains(got, "Widget") || !strings.Contains(got, "sample.go:3") {
		t.Errorf("search missing Widget/file:line:\n%s", got)
	}

	var symOut bytes.Buffer
	if err := runCodeSymbol(&symOut, root, dbPath, "Widget"); err != nil {
		t.Fatalf("symbol: %v", err)
	}
	sym := symOut.String()
	if !strings.Contains(sym, "func Widget(n int) int") || !strings.Contains(sym, "sample.go:3-3") {
		t.Errorf("symbol slice wrong:\n%s", sym)
	}
}

func TestRunCodeSearch_NoMatch(t *testing.T) {
	root, dbPath := writeCodeRepo(t)
	var idx bytes.Buffer
	if err := runCodeIndex(&idx, root, dbPath, false); err != nil {
		t.Fatalf("index: %v", err)
	}
	var out bytes.Buffer
	if err := runCodeSearch(&out, dbPath, "NoSuchThing", 25, false); err != nil {
		t.Fatalf("search: %v", err)
	}
	if !strings.Contains(out.String(), "no symbols match") {
		t.Errorf("expected no-match message, got: %q", out.String())
	}
}

// decodeSymbolsJSON parses the --json feed and fails the test on malformed output.
func decodeSymbolsJSON(t *testing.T, raw string) []codeSymbolJSON {
	t.Helper()
	var rows []codeSymbolJSON
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, raw)
	}
	return rows
}

func TestRunCodeSymbols_JSONAndText(t *testing.T) {
	root, dbPath := writeCodeRepo(t)
	var idx bytes.Buffer
	if err := runCodeIndex(&idx, root, dbPath, false); err != nil {
		t.Fatalf("index: %v", err)
	}

	// --json: every indexed symbol, valid JSON, raw fields only.
	var jsonOut bytes.Buffer
	if err := runCodeSymbols(&jsonOut, dbPath, true); err != nil {
		t.Fatalf("symbols --json: %v", err)
	}
	rows := decodeSymbolsJSON(t, jsonOut.String())
	var widget *codeSymbolJSON
	for i := range rows {
		if rows[i].Name == "Widget" {
			widget = &rows[i]
		}
	}
	if widget == nil {
		t.Fatalf("Widget missing from symbols feed:\n%s", jsonOut.String())
	}
	if widget.Kind == "" || widget.File != "sample.go" || widget.StartLine != 3 {
		t.Errorf("Widget row wrong: %+v", *widget)
	}
	// Both declarations (Widget, Gadget) are present.
	if len(rows) < 2 {
		t.Errorf("expected >=2 symbols, got %d", len(rows))
	}

	// Text mode lists symbols with file:line, like search.
	var textOut bytes.Buffer
	if err := runCodeSymbols(&textOut, dbPath, false); err != nil {
		t.Fatalf("symbols text: %v", err)
	}
	if !strings.Contains(textOut.String(), "Widget") || !strings.Contains(textOut.String(), "sample.go:3") {
		t.Errorf("text symbols missing Widget/file:line:\n%s", textOut.String())
	}
}

func TestRunCodeSymbols_EmptyIndexYieldsJSONArray(t *testing.T) {
	// A never-indexed repo: Open creates an empty db, so the feed is `[]`, not an error.
	root := t.TempDir()
	dbPath := cliconfig.Config{}.ResolveIndexDB(root)
	var out bytes.Buffer
	if err := runCodeSymbols(&out, dbPath, true); err != nil {
		t.Fatalf("symbols --json on empty index: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "[]" {
		t.Errorf("empty index should yield [], got: %q", got)
	}
}

func TestRunCodeSearch_JSONEmptyAndMatch(t *testing.T) {
	root, dbPath := writeCodeRepo(t)
	var idx bytes.Buffer
	if err := runCodeIndex(&idx, root, dbPath, false); err != nil {
		t.Fatalf("index: %v", err)
	}
	// No match → `[]`, not the human "no symbols match" line.
	var empty bytes.Buffer
	if err := runCodeSearch(&empty, dbPath, "NoSuchThing", 25, true); err != nil {
		t.Fatalf("search --json: %v", err)
	}
	if got := strings.TrimSpace(empty.String()); got != "[]" {
		t.Errorf("no-match --json should yield [], got: %q", got)
	}
	// A match → JSON array carrying the symbol.
	var hit bytes.Buffer
	if err := runCodeSearch(&hit, dbPath, "Widget", 25, true); err != nil {
		t.Fatalf("search --json: %v", err)
	}
	rows := decodeSymbolsJSON(t, hit.String())
	if len(rows) == 0 || rows[0].Name != "Widget" {
		t.Errorf("expected Widget in JSON search, got: %s", hit.String())
	}
}

func TestRunCodeSymbol_NotFound(t *testing.T) {
	root, dbPath := writeCodeRepo(t)
	var idx bytes.Buffer
	if err := runCodeIndex(&idx, root, dbPath, false); err != nil {
		t.Fatalf("index: %v", err)
	}
	var out bytes.Buffer
	if err := runCodeSymbol(&out, root, dbPath, "Missing"); err != nil {
		t.Fatalf("symbol: %v", err)
	}
	if !strings.Contains(out.String(), "no symbol named") {
		t.Errorf("expected not-found message, got: %q", out.String())
	}
}
