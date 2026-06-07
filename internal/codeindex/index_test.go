package codeindex

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIndexRepo_MultiLanguage indexes a repo with Go, Python and TypeScript
// files in one pass and checks every language's symbols land.
func TestIndexRepo_MultiLanguage(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.go"), "package main\nfunc GoFunc(){}\n")
	mustWrite(t, filepath.Join(root, "app.py"), samplePy)
	mustWrite(t, filepath.Join(root, "web.ts"), sampleTS)

	store := openTemp(t)
	stats, err := IndexRepo(store, root, false)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	if stats.FilesParsed != 3 {
		t.Errorf("FilesParsed=%d, want 3", stats.FilesParsed)
	}
	for _, name := range []string{"GoFunc", "Greeter", "makeUser", "Repo"} {
		hits, _ := store.ByName(name)
		if len(hits) == 0 {
			t.Errorf("symbol %q not indexed across languages", name)
		}
	}
}

// TestIndexRepo_IncrementalSkipsUnchanged proves the content-hash cache: a
// second run with no edits re-parses nothing, and editing one file re-parses
// only that file.
func TestIndexRepo_IncrementalSkipsUnchanged(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a.go"), "package p\nfunc A(){}\n")
	mustWrite(t, filepath.Join(root, "b.py"), "def b():\n    pass\n")
	store := openTemp(t)

	first, err := IndexRepo(store, root, false)
	if err != nil {
		t.Fatalf("first index: %v", err)
	}
	if first.FilesParsed != 2 || first.Unchanged != 0 {
		t.Fatalf("first run parsed=%d unchanged=%d, want 2/0", first.FilesParsed, first.Unchanged)
	}

	// No edits: everything is cached.
	second, err := IndexRepo(store, root, false)
	if err != nil {
		t.Fatalf("second index: %v", err)
	}
	if second.FilesParsed != 0 || second.Unchanged != 2 {
		t.Errorf("no-edit run parsed=%d unchanged=%d, want 0/2", second.FilesParsed, second.Unchanged)
	}

	// Edit one file: only it re-parses.
	mustWrite(t, filepath.Join(root, "a.go"), "package p\nfunc A(){}\nfunc A2(){}\n")
	third, err := IndexRepo(store, root, false)
	if err != nil {
		t.Fatalf("third index: %v", err)
	}
	if third.FilesParsed != 1 || third.Unchanged != 1 {
		t.Errorf("one-edit run parsed=%d unchanged=%d, want 1/1", third.FilesParsed, third.Unchanged)
	}
	if hits, _ := store.ByName("A2"); len(hits) != 1 {
		t.Errorf("new symbol A2 not picked up after incremental re-parse")
	}

	// --full forces a re-parse of everything regardless of the cache.
	full, err := IndexRepo(store, root, true)
	if err != nil {
		t.Fatalf("full index: %v", err)
	}
	if full.FilesParsed != 2 || full.Unchanged != 0 {
		t.Errorf("--full run parsed=%d unchanged=%d, want 2/0", full.FilesParsed, full.Unchanged)
	}
}

// TestIndexRepo_PrunesDeleted proves a removed file's symbols and cache row are
// dropped on the next index.
func TestIndexRepo_PrunesDeleted(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "keep.go"), "package p\nfunc Keep(){}\n")
	gone := filepath.Join(root, "gone.go")
	mustWrite(t, gone, "package p\nfunc Gone(){}\n")
	store := openTemp(t)

	if _, err := IndexRepo(store, root, false); err != nil {
		t.Fatalf("index: %v", err)
	}
	if hits, _ := store.ByName("Gone"); len(hits) != 1 {
		t.Fatalf("Gone not indexed before delete")
	}

	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	stats, err := IndexRepo(store, root, false)
	if err != nil {
		t.Fatalf("reindex: %v", err)
	}
	if stats.Deleted != 1 {
		t.Errorf("Deleted=%d, want 1", stats.Deleted)
	}
	if hits, _ := store.ByName("Gone"); len(hits) != 0 {
		t.Errorf("Gone symbol not pruned after file deletion")
	}
	if hits, _ := store.ByName("Keep"); len(hits) != 1 {
		t.Errorf("Keep symbol lost during prune")
	}
	hashes, _ := store.LoadFileHashes()
	if _, ok := hashes["gone.go"]; ok {
		t.Errorf("gone.go still in file-hash cache after prune")
	}
}
