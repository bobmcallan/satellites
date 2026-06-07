package codeindex

import "testing"

const samplePy = `import os

class Greeter:
    def __init__(self, name):
        self.name = name

    def greet(self):
        return "hi " + self.name

def main():
    Greeter("x").greet()
`

const sampleTS = `export interface User {
  id: number
  name: string
}

export function makeUser(id: number): User {
  return { id, name: "x" }
}

class Repo {
  find(id: number): User | null {
    return null
  }
}
`

func TestExtractTSFile_Python(t *testing.T) {
	syms, recognized, err := extractTSFile("sample.py", []byte(samplePy))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !recognized {
		t.Fatal("python file not recognised — grammar missing from build?")
	}
	got := kindByName(syms)
	want := map[string]string{
		"Greeter":  "class",
		"__init__": "func",
		"greet":    "func",
		"main":     "func",
	}
	for name, kind := range want {
		if got[name] != kind {
			t.Errorf("python symbol %q: kind=%q want %q (all: %v)", name, got[name], kind, got)
		}
	}
	assertSpans(t, syms)
}

func TestExtractTSFile_TypeScript(t *testing.T) {
	syms, recognized, err := extractTSFile("sample.ts", []byte(sampleTS))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !recognized {
		t.Fatal("typescript file not recognised — grammar missing from build?")
	}
	got := kindByName(syms)
	want := map[string]string{
		"User":     "interface",
		"makeUser": "func",
		"Repo":     "class",
		"find":     "method",
	}
	for name, kind := range want {
		if got[name] != kind {
			t.Errorf("ts symbol %q: kind=%q want %q (all: %v)", name, got[name], kind, got)
		}
	}
	assertSpans(t, syms)
}

func TestExtractTSFile_UnknownLanguageSkipped(t *testing.T) {
	syms, recognized, err := extractTSFile("notes.unknownext", []byte("nonsense"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recognized || len(syms) != 0 {
		t.Errorf("unknown extension should be unrecognised+empty, got recognized=%v syms=%d", recognized, len(syms))
	}
}

func TestExtractFile_DispatchesGoToAST(t *testing.T) {
	// A .go file routes through the go/ast extractor (rich func signature), not
	// tree-sitter — guards the dispatch in extractFile.
	syms, recognized, err := extractFile("x.go", []byte("package p\nfunc Foo(a int) int { return a }\n"))
	if err != nil || !recognized {
		t.Fatalf("go dispatch: recognized=%v err=%v", recognized, err)
	}
	sig := ""
	for _, s := range syms {
		if s.Name == "Foo" {
			sig = s.Signature
		}
	}
	if sig != "func Foo(a int) int" {
		t.Errorf("go signature = %q, want full go/ast signature", sig)
	}
}

func kindByName(syms []Symbol) map[string]string {
	out := map[string]string{}
	for _, s := range syms {
		out[s.Name] = s.Kind
	}
	return out
}

func assertSpans(t *testing.T, syms []Symbol) {
	t.Helper()
	for _, s := range syms {
		if s.StartLine == 0 || s.EndByte <= s.StartByte || s.File == "" {
			t.Errorf("symbol %q has bad span: %+v", s.Name, s)
		}
	}
}
