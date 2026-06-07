package codeindex

import (
	"fmt"
	"os"
	"path/filepath"
)

// ReadSlice returns the exact source bytes a symbol spans, read from its file
// under repoRoot using the stored byte offsets. Byte offsets give a precise
// slice independent of how the file is re-read; if they fall outside the current
// file (the file changed since indexing) it returns an error rather than a wrong
// slice, so a stale index surfaces instead of misleading.
func ReadSlice(repoRoot string, sym Symbol) ([]byte, error) {
	path := filepath.Join(repoRoot, filepath.FromSlash(sym.File))
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("codeindex: read %s: %w", sym.File, err)
	}
	if sym.StartByte < 0 || sym.EndByte > len(src) || sym.StartByte > sym.EndByte {
		return nil, fmt.Errorf("codeindex: %s offsets [%d:%d] out of range for %s (%d bytes) — re-run `code index`",
			sym.Name, sym.StartByte, sym.EndByte, sym.File, len(src))
	}
	return src[sym.StartByte:sym.EndByte], nil
}
