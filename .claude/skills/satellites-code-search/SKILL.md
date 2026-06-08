<!-- satellites-sync:begin {"document_id":"doc_b6d7819b","version":1,"hash":"e4d76787dd4e490fe87aa167d89888262354d962527ce9484425f7c2bd4b2acf"} satellites-sync:end -->
---
name: satellites-code-search
type: skill
kind: capability
when: code-discovery
tags: [kind:capability]
description: Prefer the in-client code index for code discovery — use `satellites code search`/`code symbol` over Read/Grep to find where a symbol is defined, read a declaration's body, or navigate code. Index once per session with `satellites code index`. Invoke whenever a task means locating or reading code by name (where is X / what is X's body / what is this type).
---

# satellites-code-search

The repo has a first-party symbol index. For **code discovery**, reach for it
before Read/Grep: it returns exact `file:line` and source slices at a fraction of
the tokens of reading whole files (the order:1 spike measured ~10× less content
on a representative navigation question).

## The commands

```bash
satellites code index            # build/refresh .satellites/index.db (run once per session, or after large edits)
satellites code search <query>   # list matching symbols: kind, signature, file:line
satellites code symbol <name>    # print the exact source slice for a symbol by name
```

`code index` is incremental — re-running only re-parses changed files, so it is
cheap to keep current. Add `--full` to force a clean rebuild.

## The rule

For **code discovery — "where is X defined", "show me X's body", "what is this
type/function"** — prefer `satellites code search` / `satellites code symbol`
over `Read` (whole files) and `Grep` (raw text). Typical loop:

1. `satellites code search <name>` → find the symbol and its `file:line`.
2. `satellites code symbol <name>` → read just that declaration's slice.
3. Open the file with Read only if you then need surrounding context.

## Boundaries — when Grep/Read still win

The index holds **named declarations** (functions, methods, types, classes,
interfaces, …), not arbitrary text. So:

- Finding a **string literal, flag, log line, comment, or config key** → use
  `Grep` (the index won't match non-symbol text).
- Reading a **specific region** you already located, or a non-source file → use
  `Read`.
- A language whose grammar is not embedded in this build → `code search` returns
  nothing for it; fall back to Grep/Read.

The index complements Grep/Read; it does not replace them. When in doubt for a
symbol lookup, try `code search` first — it is the cheapest probe.
