package codeindex

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/odvcencio/gotreesitter/grammars"
)

// skipDirs are directories never walked: VCS metadata, the satellites working
// tree (its own index.db lives here), and vendored / dependency trees whose
// symbols are noise for discovering THIS repo's code.
var skipDirs = map[string]bool{
	".git":         true,
	".satellites":  true,
	"vendor":       true,
	"node_modules": true,
}

// IndexStats summarises one IndexRepo run for honest reporting.
type IndexStats struct {
	TotalSymbols int // symbols in the index after the run
	FilesParsed  int // files (re)parsed this run
	Unchanged    int // files skipped via the content-hash cache
	Deleted      int // indexed files no longer present, pruned
	ParseErrors  int // files an extractor could not parse (skipped, non-fatal)
}

// IndexRepo reconciles the symbol index for the repo rooted at root against the
// store, re-parsing only files whose content hash changed since the last run —
// unless full is set, which re-parses every recognised file. Files no longer
// present are pruned. The extractor is chosen per file: Go via go/ast, every
// other recognised language via the pure-Go gotreesitter runtime. Unreadable or
// unparseable files are skipped (counted), never fatal.
func IndexRepo(store *Store, root string, full bool) (IndexStats, error) {
	var stats IndexStats
	prior, err := store.LoadFileHashes()
	if err != nil {
		return stats, err
	}
	seen := make(map[string]bool, len(prior))
	var changed []FileSymbols

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel := relSlash(root, path)
		if !isIndexable(rel) {
			return nil // no extractor for this path — never read it
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil // unreadable — skip; pruned later if it was indexed
		}
		sum := hashBytes(src)
		seen[rel] = true
		if !full {
			if prev, ok := prior[rel]; ok && prev == sum {
				stats.Unchanged++
				return nil // unchanged since last index — keep existing rows
			}
		}
		syms, recognized, perr := extractFile(rel, src)
		if !recognized {
			delete(seen, rel) // detection disagreed with isIndexable — don't cache
			return nil
		}
		if perr != nil {
			stats.ParseErrors++
			delete(seen, rel) // leave uncached so a later fix re-parses it
			return nil
		}
		stats.FilesParsed++
		changed = append(changed, FileSymbols{File: rel, Sha: sum, Syms: syms})
		return nil
	})
	if walkErr != nil {
		return stats, walkErr
	}

	var deleted []string
	for f := range prior {
		if !seen[f] {
			deleted = append(deleted, f)
		}
	}
	stats.Deleted = len(deleted)

	if err := store.Reconcile(changed, deleted); err != nil {
		return stats, err
	}
	n, err := store.Count()
	if err != nil {
		return stats, err
	}
	stats.TotalSymbols = n
	return stats, nil
}

// ExtractRepo walks root and extracts top-level symbols from every recognised
// source file in one pass (no incremental cache), returning them with
// repo-relative, forward-slashed paths. Parse errors on a single file are
// counted and skipped, not fatal. It is the whole-repo extraction primitive
// behind Store.Replace; IndexRepo is the incremental path the CLI uses.
func ExtractRepo(root string) (syms []Symbol, filesParsed, parseErrors int, err error) {
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel := relSlash(root, path)
		if !isIndexable(rel) {
			return nil
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		fileSyms, recognized, perr := extractFile(rel, src)
		if !recognized {
			return nil
		}
		if perr != nil {
			parseErrors++
			return nil
		}
		filesParsed++
		syms = append(syms, fileSyms...)
		return nil
	})
	return syms, filesParsed, parseErrors, walkErr
}

// extractFile dispatches one source file to the right extractor: Go keeps the
// native go/ast extractor (rich signatures); every other recognised language
// goes through the gotreesitter runtime. recognized=false means no extractor
// matched, so the caller skips the file silently.
func extractFile(rel string, src []byte) (syms []Symbol, recognized bool, err error) {
	if strings.HasSuffix(rel, ".go") {
		s, e := extractGoFile(rel, src)
		return s, true, e
	}
	return extractTSFile(rel, src)
}

// isIndexable reports whether some extractor handles this path, by extension
// only (no read): Go, or any language whose grammar is embedded in this build.
func isIndexable(rel string) bool {
	if strings.HasSuffix(rel, ".go") {
		return true
	}
	return grammars.DetectLanguage(rel) != nil
}

func relSlash(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	return filepath.ToSlash(rel)
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
