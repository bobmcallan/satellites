package codeindex

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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

// ExtractRepo walks root and extracts top-level symbols from every Go file,
// returning them with repo-relative, forward-slashed paths. Parse errors on a
// single file are skipped (best-effort over a repo that may not fully compile),
// not fatal — the count of files that failed to parse is returned for honest
// reporting. This is the spike's Go-only extractor; order:2 replaces it with a
// language-dispatching WASM tree-sitter extractor behind the same return shape.
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
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil // unreadable file — skip, not fatal
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)
		fileSyms, perr := extractGoFile(rel, src)
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

// extractGoFile parses one Go source file and returns its top-level symbols. The
// repo-relative path is carried in for the File field; byte/line offsets are
// resolved against a per-file FileSet.
func extractGoFile(relPath string, src []byte) ([]Symbol, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, relPath, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var out []Symbol
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			out = append(out, funcSymbol(fset, relPath, d))
		case *ast.GenDecl:
			out = append(out, genDeclSymbols(fset, relPath, d)...)
		}
	}
	return out, nil
}

func funcSymbol(fset *token.FileSet, relPath string, d *ast.FuncDecl) Symbol {
	kind := "func"
	if d.Recv != nil {
		kind = "method"
	}
	// Signature = the declaration with its body stripped: `func (r R) Name(a) b`.
	sig := printNode(fset, &ast.FuncDecl{Recv: d.Recv, Name: d.Name, Type: d.Type})
	return symbolFromNode(fset, relPath, d.Name.Name, kind, sig, d)
}

func genDeclSymbols(fset *token.FileSet, relPath string, d *ast.GenDecl) []Symbol {
	var out []Symbol
	for _, spec := range d.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			out = append(out, symbolFromNode(fset, relPath, s.Name.Name, typeKind(s), typeSignature(s), s))
		case *ast.ValueSpec:
			kind := "var"
			if d.Tok == token.CONST {
				kind = "const"
			}
			for _, name := range s.Names {
				out = append(out, symbolFromNode(fset, relPath, name.Name, kind, valueSignature(kind, name, s), name))
			}
		}
	}
	return out
}

// typeKind distinguishes struct / interface from a plain type/alias so search
// can match on kind (e.g. `code search interface`).
func typeKind(s *ast.TypeSpec) string {
	switch s.Type.(type) {
	case *ast.StructType:
		return "struct"
	case *ast.InterfaceType:
		return "interface"
	default:
		return "type"
	}
}

func typeSignature(s *ast.TypeSpec) string {
	switch s.Type.(type) {
	case *ast.StructType:
		return "type " + s.Name.Name + " struct"
	case *ast.InterfaceType:
		return "type " + s.Name.Name + " interface"
	default:
		return "type " + s.Name.Name
	}
}

func valueSignature(kind string, name *ast.Ident, s *ast.ValueSpec) string {
	sig := kind + " " + name.Name
	if s.Type != nil {
		return sig // a printed type expr can be large; the kind+name is enough for recall
	}
	return sig
}

// symbolFromNode resolves a node's byte/line span into a Symbol. node.Pos() and
// node.End() are FileSet positions; Offset is the byte offset for an exact slice
// and Line is the human-facing line number.
func symbolFromNode(fset *token.FileSet, relPath, name, kind, sig string, node ast.Node) Symbol {
	start := fset.Position(node.Pos())
	end := fset.Position(node.End())
	return Symbol{
		Name:      name,
		Kind:      kind,
		Signature: sig,
		File:      relPath,
		StartLine: start.Line,
		EndLine:   end.Line,
		StartByte: start.Offset,
		EndByte:   end.Offset,
	}
}

func printNode(fset *token.FileSet, node ast.Node) string {
	var buf bytes.Buffer
	cfg := printer.Config{Mode: printer.UseSpaces, Tabwidth: 4}
	if err := cfg.Fprint(&buf, fset, node); err != nil {
		return ""
	}
	return strings.TrimSpace(buf.String())
}
