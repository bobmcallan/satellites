package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	return root, filepath.Join(root, defaultIndexDB)
}

func TestRunCodeIndexThenSearchAndSymbol(t *testing.T) {
	root, dbPath := writeCodeRepo(t)

	var idxOut bytes.Buffer
	if err := runCodeIndex(&idxOut, root, dbPath); err != nil {
		t.Fatalf("index: %v", err)
	}
	if !strings.Contains(idxOut.String(), "indexed") {
		t.Errorf("index output unexpected: %q", idxOut.String())
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("index.db not written: %v", err)
	}

	var searchOut bytes.Buffer
	if err := runCodeSearch(&searchOut, dbPath, "Widget", 25); err != nil {
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
	if err := runCodeIndex(&idx, root, dbPath); err != nil {
		t.Fatalf("index: %v", err)
	}
	var out bytes.Buffer
	if err := runCodeSearch(&out, dbPath, "NoSuchThing", 25); err != nil {
		t.Fatalf("search: %v", err)
	}
	if !strings.Contains(out.String(), "no symbols match") {
		t.Errorf("expected no-match message, got: %q", out.String())
	}
}

func TestRunCodeSymbol_NotFound(t *testing.T) {
	root, dbPath := writeCodeRepo(t)
	var idx bytes.Buffer
	if err := runCodeIndex(&idx, root, dbPath); err != nil {
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
