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
  source slice, cheaper than reading a whole file. `code search <q> --json` and
  `code symbols --json` emit the raw symbol rows (name, kind, signature, file, lines) as
  data from `index.db` — no re-parse.
- `code map` — entry-point reachability.
- `code graph` — the package import graph. Query it instead of eyeballing the dump:
  `--package <p>` (direct in/out edges), `--rdeps <p>` (blast radius — who imports `<p>`
  transitively), `--deps <p>` (what `<p>` pulls in), `--cycles`; each composes with
  `--json`.

**Constructing a richer graph.** To compose a deeper codegraph (real per-package counts,
not the C# extractor's `0`s), join two raw JSON feeds the binary already emits and let the
agent do the interpretation: `code graph --json` is the package/edge **spine**, and
`code symbols --json` is the per-symbol **fill** (attribute symbols to packages by `file`,
count public types from `signature`). The binary stays raw facts; the agent renders the
JGF. Publish the result as the `type:codegraph` document (`format:jgf-v1`).

**Local vs MCP split.** Generation and search run LOCALLY (working tree +
`.satellites/index.db`) and are CLI-only — there is NO MCP code-search or code-generate
verb, because the remote MCP server has no working tree. The MCP-reachable surface is the
PUBLISHED artifact: the `type:codegraph` document the Codegraph task emits — reach it with
`document_list {tags:["type:codegraph"]}` → `document_get`, or `semantic_search`.

**The Codegraph task — consume it, don't hand-author it.** The Codegraph task (the re-runnable
job that renders `code graph --json` into the published `type:codegraph` document) is published
ONCE to the task library, so a fresh repo does NOT author its own: add the codegraph publisher to
`global_publishers` in `.satellites/satellites.toml`, run `satellites task sync` to materialise it
as a project task, then run it. Re-run the task whenever the codebase structure changes.

**Three independent subsystems.** `codeindex` (symbol search, backed by `index.db`) ·
`codemap` (entry-point reachability) · `codegraph` (package import graph; does NOT use
`index.db`). They answer different questions — pick by what you need.

See [[agent-goals]].
