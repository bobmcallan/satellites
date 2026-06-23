---
name: code-intelligence
type: document
scope: system
tags: [principles:global, principles:always]
---

# Code intelligence

Satellites ships LOCAL code-intelligence CLI verbs over the working tree — prefer them
over Read/Grep for STRUCTURE. Grep still wins for free text, comments, and config; the
verbs win for declarations, structure, and blast radius.

- `satellites code index` — refresh the symbol index first (the SessionStart hook also
  runs it).
- `code search <q>` / `code symbol <name>` — find declarations / print a symbol's exact
  source slice, cheaper than reading a whole file.
- `code map` — entry-point reachability.
- `code graph` — the package import graph. Query it instead of eyeballing the dump:
  `--package <p>` (direct in/out edges), `--rdeps <p>` (blast radius — who imports `<p>`
  transitively), `--deps <p>` (what `<p>` pulls in), `--cycles`; each composes with
  `--json`.

**Local vs MCP split.** Generation and search run LOCALLY (working tree +
`.satellites/index.db`) and are CLI-only — there is NO MCP code-search or code-generate
verb, because the remote MCP server has no working tree. The MCP-reachable surface is the
PUBLISHED artifact: the `type:codegraph` document the Codegraph task emits — reach it with
`document_list {tags:["type:codegraph"]}` → `document_get`, or `semantic_search`.

**Three independent subsystems.** `codeindex` (symbol search, backed by `index.db`) ·
`codemap` (entry-point reachability) · `codegraph` (package import graph; does NOT use
`index.db`). They answer different questions — pick by what you need.

See [[agent-goals]].
