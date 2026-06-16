---
name: satellites-code-search
type: skill
kind: capability
when: code-discovery
tags: [kind:capability]
description: Prefer the in-client code index for code discovery — use `satellites code search`/`code symbol` over Read/Grep to find where a symbol is defined, read a declaration's body, or navigate code. Index once per session with `satellites code index`. Invoke whenever a task means locating or reading code by name (where is X / what is X's body / what is this type).
---
<!-- satellites-sync:begin {"document_id":"doc_e9a845a3","version":3,"hash":"94f1c249615f933093889ea86a56f60173aa40e57385649540facd3944194efd","publisher":"proj_682cfeed"} satellites-sync:end -->
<!-- satellites-library:begin {"publisher":"proj_682cfeed","repo":"https://github.com/bobmcallan/satellites-skills","commit":"7caa10cbeb50ac1856b1576e7ffbdafc7ca746eb"} satellites-library:end -->

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
