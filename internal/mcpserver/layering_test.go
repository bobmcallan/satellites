package mcpserver

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoSubstrateDomainImports enforces pr_mcp_cli_shared_path: the
// transport layer (this package) must NOT import substrate-domain
// packages directly. Every business-logic touch flows through
// internal/verb's Dispatch. The CLI (the other transport) is
// also forbidden — transports should not call each other.
func TestNoSubstrateDomainImports(t *testing.T) {
	forbidden := []string{
		"github.com/bobmcallan/satellites/internal/db",
		"github.com/bobmcallan/satellites/internal/cli",
		"github.com/bobmcallan/satellites/internal/ledger",
		// Future substrate packages get added here as they appear.
	}

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no source files found in package")
	}

	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		a, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		for _, imp := range a.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			for _, p := range forbidden {
				if path == p || strings.HasPrefix(path, p+"/") {
					t.Errorf("%s imports forbidden substrate-domain package: %s", f, path)
				}
			}
		}
	}
}
