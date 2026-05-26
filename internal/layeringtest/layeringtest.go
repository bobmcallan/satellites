// Package layeringtest is the shared AST import-guard used by every
// transport package (internal/cli, internal/mcpserver, internal/server)
// to enforce pr_mcp_cli_shared_path: transports route through
// internal/verb's Dispatch and must NOT import substrate-domain
// packages or peer transports directly.
//
// The constant ForbiddenDomain lists every package transports are
// banned from importing. RunGuard scans every non-test .go file in
// the caller's package directory and fails the test if any import
// matches ForbiddenDomain or one of the extra paths the caller passes
// (for the peer-transport bans).
package layeringtest

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// ForbiddenDomain is the canonical list of substrate-domain packages
// that no transport may import. Add entries here as new domain
// packages appear; all three transport guards pick the change up
// automatically.
var ForbiddenDomain = []string{
	"github.com/bobmcallan/satellites/internal/db",
	"github.com/bobmcallan/satellites/internal/document",
	"github.com/bobmcallan/satellites/internal/ledger",
	"github.com/bobmcallan/satellites/internal/project",
	"github.com/bobmcallan/satellites/internal/reviewer",
	"github.com/bobmcallan/satellites/internal/variable",
	"github.com/bobmcallan/satellites/internal/workspace",
}

// RunGuard scans every non-test .go file in the caller's package
// directory and fails t if any import matches ForbiddenDomain or
// extraForbidden (typically the peer-transport packages — a transport
// must not import another transport). Call from each transport's
// layering_test.go.
func RunGuard(t *testing.T, extraForbidden ...string) {
	t.Helper()
	forbidden := append([]string{}, ForbiddenDomain...)
	forbidden = append(forbidden, extraForbidden...)

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
					t.Errorf("%s imports forbidden package: %s", f, path)
				}
			}
		}
	}
}
