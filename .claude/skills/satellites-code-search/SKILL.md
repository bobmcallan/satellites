<!-- satellites-sync:begin {"document_id":"doc_b6d7819b","version":2,"hash":"802779d956c0e14f31ac1220f4a9a45b123b8b993aca2a972fe2818dcdf9b8b3"} satellites-sync:end -->
---
name: satellites-code-search
type: skill
kind: capability
when: code-discovery
tags: [kind:capability]
description: Prefer the in-client code index for code discovery — use `satellites code search`/`code symbol` over Read/Grep to find where a symbol is defined, read a declaration's body, or navigate code. Index once per session with `satellites code index`. Invoke whenever a task means locating or reading code by name (where is X / what is X's body / what is this type).
---

# satellites-code-search

For code discovery — "where is X defined", "show me X's body", "what is this
type/function" — prefer the symbol index over Read (whole files) and Grep (raw
text).

```bash
satellites code index            # build/refresh the index; run once per session, or after large edits (--full to force a clean rebuild)
satellites code search <query>   # list matching symbols: kind, signature, file:line
satellites code symbol <name>    # print the exact source slice for a symbol by name
```

Typical loop:

1. `satellites code search <name>` → find the symbol and its `file:line`.
2. `satellites code symbol <name>` → read just that declaration's slice.
3. Open the file with Read only if you then need surrounding context.

## When Grep/Read still win

The index holds named declarations (functions, methods, types, classes,
interfaces), not arbitrary text:

- String literal, flag, log line, comment, or config key → use `Grep`.
- A specific region you already located, or a non-source file → use `Read`.
- A language whose grammar is not embedded in the build (`code search` returns
  nothing) → fall back to Grep/Read.
