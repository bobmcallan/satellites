package codeindex

import (
	"bytes"

	ts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// tsKindByType maps a tree-sitter declaration node type to the index's
// language-neutral kind. It spans the major grammars' top-level declaration
// nodes; an unlisted node type is not emitted as a symbol. Keys are tree-sitter
// node-type strings (stable per grammar). One node type can mean the same kind
// across several languages (e.g. function_definition in python, c, cpp, php).
var tsKindByType = map[string]string{
	// functions
	"function_definition":  "func", // python, c, cpp, php
	"function_declaration": "func", // javascript, typescript, c
	"function_item":        "func", // rust
	// methods
	"method_definition":       "method", // javascript, typescript
	"method_declaration":      "method", // java, c#, php
	"method":                  "method", // ruby (def)
	"constructor_declaration": "method", // java, c#
	// classes / types
	"class_definition":       "class",     // python
	"class_declaration":      "class",     // js, ts, java, c#, php
	"class_specifier":        "class",     // cpp
	"struct_specifier":       "struct",    // c, cpp
	"struct_item":            "struct",    // rust
	"interface_declaration":  "interface", // ts, java, c#
	"enum_declaration":       "enum",      // ts, java, c#
	"enum_specifier":         "enum",      // c, cpp
	"enum_item":              "enum",      // rust
	"trait_item":             "trait",     // rust
	"type_alias_declaration": "type",      // ts
	"type_item":              "type",      // rust
	"mod_item":               "module",    // rust
	"module":                 "module",    // ruby
	"namespace_definition":   "namespace", // cpp
}

// tsNameNodeTypes are identifier-class node types used to recover a declaration's
// name when the grammar exposes no "name" field.
var tsNameNodeTypes = map[string]bool{
	"identifier":          true,
	"type_identifier":     true,
	"constant":            true, // ruby class/module names
	"property_identifier": true,
	"field_identifier":    true,
	"scoped_identifier":   true,
}

// extractTSFile extracts top-level declaration symbols from a non-Go source file
// using the pure-Go gotreesitter runtime, dispatching by file extension/shebang.
// recognized=false means no embedded grammar matched the path (caller skips it);
// a parse failure returns recognized=true with err so it is counted, not fatal.
func extractTSFile(rel string, src []byte) (syms []Symbol, recognized bool, err error) {
	entry := grammars.DetectLanguage(rel)
	if entry == nil || entry.Language == nil {
		return nil, false, nil
	}
	lang := entry.Language()
	if lang == nil {
		return nil, false, nil // grammar not embedded in this build — skip
	}
	parser := ts.NewParser(lang)
	tree, perr := parser.Parse(src)
	if perr != nil {
		return nil, true, perr
	}
	root := tree.RootNode()
	if root == nil {
		return nil, true, nil
	}
	var out []Symbol
	walkTSNode(root, lang, rel, src, &out)
	return out, true, nil
}

// walkTSNode emits a Symbol for every declaration-kind node in the subtree,
// recursing through named children so a class and its methods are both indexed.
func walkTSNode(n *ts.Node, lang *ts.Language, rel string, src []byte, out *[]Symbol) {
	if kind, ok := tsKindByType[n.Type(lang)]; ok {
		if name := tsNodeName(n, lang, src); name != "" {
			start, end := int(n.StartByte()), int(n.EndByte())
			*out = append(*out, Symbol{
				Name:      name,
				Kind:      kind,
				Signature: tsSignature(src, start, end),
				File:      rel,
				StartLine: int(n.StartPoint().Row) + 1,
				EndLine:   int(n.EndPoint().Row) + 1,
				StartByte: start,
				EndByte:   end,
			})
		}
	}
	for i := 0; i < n.NamedChildCount(); i++ {
		walkTSNode(n.NamedChild(i), lang, rel, src, out)
	}
}

// tsNodeName returns a declaration's identifier: the "name" field if the grammar
// exposes one, else the first child that is an identifier-class node.
func tsNodeName(n *ts.Node, lang *ts.Language, src []byte) string {
	if nm := n.ChildByFieldName("name", lang); nm != nil {
		return nm.Text(src)
	}
	for i := 0; i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if tsNameNodeTypes[c.Type(lang)] {
			return c.Text(src)
		}
	}
	return ""
}

// tsSignature is the first line of a node's source span, trimmed and capped —
// enough to identify the declaration without storing a whole body.
func tsSignature(src []byte, start, end int) string {
	if start < 0 || end > len(src) || start >= end {
		return ""
	}
	slice := src[start:end]
	if nl := bytes.IndexByte(slice, '\n'); nl >= 0 {
		slice = slice[:nl]
	}
	const max = 200
	if len(slice) > max {
		slice = slice[:max]
	}
	return string(bytes.TrimSpace(slice))
}
