package codeindex

import (
	"os"
	"path/filepath"
	"testing"
)

// sampleGo is a small Go file exercising every extracted kind.
const sampleGo = `package sample

import "fmt"

// Greeter greets.
type Greeter interface {
	Greet() string
}

type Person struct {
	Name string
}

const MaxRetries = 3

var DefaultName = "world"

func Hello(name string) string {
	return fmt.Sprintf("hello %s", name)
}

func (p Person) Greet() string {
	return "hi " + p.Name
}
`

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestExtractGoFile_Kinds(t *testing.T) {
	syms, err := extractGoFile("sample.go", []byte(sampleGo))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	got := map[string]string{} // name -> kind
	for _, s := range syms {
		got[s.Name] = s.Kind
		if s.File != "sample.go" || s.StartLine == 0 || s.EndByte <= s.StartByte {
			t.Errorf("symbol %q has bad span: %+v", s.Name, s)
		}
	}
	want := map[string]string{
		"Greeter":     "interface",
		"Person":      "struct",
		"MaxRetries":  "const",
		"DefaultName": "var",
		"Hello":       "func",
		"Greet":       "method",
	}
	for name, kind := range want {
		if got[name] != kind {
			t.Errorf("symbol %q: kind = %q, want %q", name, got[name], kind)
		}
	}
}

func TestReplaceAndSearch_FTSPrefix(t *testing.T) {
	s := openTemp(t)
	syms, _ := extractGoFile("sample.go", []byte(sampleGo))
	if err := s.Replace(syms); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if n, _ := s.Count(); n != len(syms) {
		t.Fatalf("count = %d, want %d", n, len(syms))
	}
	// Whole-identifier prefix: "Hel" matches Hello.
	hits, err := s.Search("Hel", 0)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !containsName(hits, "Hello") {
		t.Errorf("prefix search Hel did not recall Hello: %+v", names(hits))
	}
}

func TestSearch_LikeFallback(t *testing.T) {
	s := openTemp(t)
	syms, _ := extractGoFile("sample.go", []byte(sampleGo))
	_ = s.Replace(syms)
	// "etrie" is mid-identifier (MaxRetries) — no FTS prefix token matches, so
	// the LIKE substring fallback must recall it.
	hits, err := s.Search("etrie", 0)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !containsName(hits, "MaxRetries") {
		t.Errorf("substring fallback did not recall MaxRetries: %+v", names(hits))
	}
}

func TestSearch_Limit(t *testing.T) {
	s := openTemp(t)
	syms, _ := extractGoFile("sample.go", []byte(sampleGo))
	_ = s.Replace(syms)
	hits, err := s.Search("e", 2) // broad substring, capped
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) > 2 {
		t.Errorf("limit not honoured: got %d, want <=2", len(hits))
	}
}

func TestByNameAndReadSlice(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte(sampleGo), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(root, ".satellites", "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	syms, _, _, err := ExtractRepo(root)
	if err != nil {
		t.Fatalf("extract repo: %v", err)
	}
	if err := s.Replace(syms); err != nil {
		t.Fatalf("replace: %v", err)
	}

	hits, err := s.ByName("Hello")
	if err != nil || len(hits) != 1 {
		t.Fatalf("ByName Hello: %v, %d hits", err, len(hits))
	}
	slice, err := ReadSlice(root, hits[0])
	if err != nil {
		t.Fatalf("read slice: %v", err)
	}
	if got := string(slice); !startsWith(got, "func Hello(") {
		t.Errorf("slice not the Hello func body:\n%s", got)
	}
}

func TestReadSlice_StaleOffsets(t *testing.T) {
	root := t.TempDir()
	sym := Symbol{Name: "X", File: "gone.go", StartByte: 0, EndByte: 100}
	if _, err := ReadSlice(root, sym); err == nil {
		t.Error("expected error reading slice from missing file")
	}
	if err := os.WriteFile(filepath.Join(root, "short.go"), []byte("ab"), 0o644); err != nil {
		t.Fatal(err)
	}
	sym = Symbol{Name: "X", File: "short.go", StartByte: 0, EndByte: 100}
	if _, err := ReadSlice(root, sym); err == nil {
		t.Error("expected out-of-range error for stale offsets")
	}
}

func TestExtractRepo_SkipsDirs(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.go"), "package main\nfunc Real(){}\n")
	mustWrite(t, filepath.Join(root, "vendor", "dep.go"), "package dep\nfunc Vendored(){}\n")
	mustWrite(t, filepath.Join(root, ".git", "hook.go"), "package x\nfunc Hidden(){}\n")
	syms, files, _, err := ExtractRepo(root)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if files != 1 {
		t.Errorf("walked %d files, want 1 (vendor/.git skipped)", files)
	}
	if containsName(syms, "Vendored") || containsName(syms, "Hidden") {
		t.Errorf("skip-dir leak: %+v", names(syms))
	}
	if !containsName(syms, "Real") {
		t.Errorf("did not index real source: %+v", names(syms))
	}
}

func TestExtractRepo_ParseErrorNonFatal(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "ok.go"), "package p\nfunc Ok(){}\n")
	mustWrite(t, filepath.Join(root, "bad.go"), "package p\nthis is not go\n")
	syms, files, parseErrors, err := ExtractRepo(root)
	if err != nil {
		t.Fatalf("extract should not fail on one bad file: %v", err)
	}
	if files != 1 || parseErrors != 1 {
		t.Errorf("files=%d parseErrors=%d, want 1/1", files, parseErrors)
	}
	if !containsName(syms, "Ok") {
		t.Errorf("good file not indexed past bad one: %+v", names(syms))
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func containsName(syms []Symbol, name string) bool {
	for _, s := range syms {
		if s.Name == name {
			return true
		}
	}
	return false
}

func names(syms []Symbol) []string {
	out := make([]string, len(syms))
	for i, s := range syms {
		out[i] = s.Name
	}
	return out
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
