// Package common provides test-only helpers shared across the integration
// suites under tests/.
//
// On import, this package's init walks from its own source location up to
// the nearest ancestor directory named "tests" and reads "<that-dir>/.env"
// when present. A short whitelist of keys (Gemini reviewer + embeddings
// provider) is propagated into the process environment so integration
// tests that drive a real LLM can pick up credentials without an explicit
// shell export. Host-exported values always win — the loader never
// overwrites a key that is already set in the process env.
//
// The walk is bounded at the "tests" ancestor by design: dotenv loading
// is a test-only convenience, and the substrate's production code paths
// are deliberately env-var-direct (no godotenv import). The bound prevents
// a stray repo-root ".env" from leaking into go-test environments.
package common

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// whitelistedDotenvKeys names every variable the loader is allowed to
// push into the process environment from tests/.env. Anything outside the
// list is silently ignored even if present in the file.
var whitelistedDotenvKeys = map[string]struct{}{
	"GEMINI_API_KEY":      {},
	"GEMINI_REVIEW_MODEL": {},
	"EMBEDDINGS_API_KEY":  {},
	"EMBEDDINGS_PROVIDER": {},
	"EMBEDDINGS_MODEL":    {},
}

func init() {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return
	}
	path := findTestsEnvFile(filepath.Dir(thisFile))
	if path == "" {
		return
	}
	loadDotenvFile(path)
}

// findTestsEnvFile walks upward from start, stopping at the first
// ancestor whose basename is "tests". When that ancestor exists and
// contains a ".env" file, returns its absolute path; otherwise returns
// "". Walks at most a handful of levels — the loader refuses to escape
// the tests/ subtree.
func findTestsEnvFile(start string) string {
	dir := start
	for i := 0; i < 20; i++ {
		if filepath.Base(dir) == "tests" {
			candidate := filepath.Join(dir, ".env")
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// loadDotenvFile parses KEY=VALUE lines from path. Optional surrounding
// quotes and a leading "export " are tolerated; comments and blank lines
// are skipped; malformed lines (no "=" or empty key) are ignored. Each
// key is propagated to the process environment iff it is whitelisted and
// currently empty (host-exported values win).
func loadDotenvFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if _, ok := whitelistedDotenvKeys[key]; !ok {
			continue
		}
		if len(val) >= 2 {
			first, last := val[0], val[len(val)-1]
			if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		if os.Getenv(key) != "" {
			continue
		}
		_ = os.Setenv(key, val)
	}
}
