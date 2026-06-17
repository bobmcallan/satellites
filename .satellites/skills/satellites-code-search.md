---
name: satellites-code-search
type: skill
kind: capability
when: code-discovery
tags: [kind:capability]
description: Prefer the in-client code index for code discovery — use `satellites code search`/`code symbol` over Read/Grep (and over `grep`/`rg` run via Bash) to find where a symbol is defined, read a declaration's body, or navigate code. Covers delegated/sub-agent search too. Index once per session with `satellites code index`. Invoke whenever a task means locating or reading code by name (where is X / what is X's body / what is this type).
---

# satellites-code-search

For code discovery — "where is X defined", "show me X's body", "what is this
type/function" — prefer the symbol index over Read (whole files) and Grep / raw
`grep`/`rg` (text scans).

```bash
satellites code index            # build/refresh the index; run once per session, or after large edits (--full to force a clean rebuild)
satellites code search <query>   # list matching symbols: kind, signature, file:line
satellites code symbol <name>    # print the exact source slice for a symbol by name
```

Typical loop:

1. `satellites code search <name>` → find the symbol and its `file:line`.
2. `satellites code symbol <name>` → read just that declaration's slice.
3. Open the file with Read only if you then need surrounding context.

## Delegated / sub-agent search

The same rule binds delegated work. When you hand a sub-agent (e.g. an `Explore`
or `general-purpose` agent) a "where is X / show me X's body / what is this type"
task, instruct it to use `satellites code search`/`code symbol` — not Grep or
Read. A sub-agent that defaults to scanning files re-reads what the index already
has indexed, burning context for the same answer. Name the index in the task you
delegate.

## Language coverage

The index extracts symbols per file: **Go** via the native `go/ast` parser, and
**every other language** via a CGo-free WASM tree-sitter runtime (~100+
grammars), so coverage is broad — not Go-only. `code search` returning nothing
for a file usually means an unindexed grammar or a non-declaration target, not a
missing feature.

## When Grep/Read still win

The index holds named declarations (functions, methods, types, classes,
interfaces), not arbitrary text:

- String literal, flag, log line, comment, or config key → use `Grep`.
- A specific region you already located, or a non-source file → use `Read`.
- A language whose grammar is not indexed (`code search` returns nothing) →
  fall back to Grep/Read.
