---
name: satellites_mcp_load_context
scope: system
tags: [kind:mcp-startup, v2]
---
# satellites · bootstrap

**To work a story:** `document_get` it, read the `kind:workflow` skill it names
(that skill IS the process — its `## Workflow` lists the gated transitions), and
drive each transition with `satellites story status_transition --skill <gate>`.
Fetch everything else only when a task needs it — **do not preload.**

## Prefixes

`document:<scope>/<name>` → `document_get` · `project:<id>` → `project_get` ·
`story:<id>` → `document_get` · `epic:<slug>` → `document_list {tags:["epic:<slug>"]}` ·
`variable:<scope>/<name>` → `variable_get`

## First-time setup (once per repo)

`document_get {name:"satellites_client_install", scope:"system", os, arch, current_version}`
and follow it — install, auth, TOML, `project_match`.

## Session start

- **Skills:** `satellites skill sync` pulls every scope into `.claude/skills/`
  (stamp-reconciled). Author a NEW skill in `.satellites/skills/`, then
  `satellites skill upload` (review-gated); never hand-write `.claude/skills/`.
- **Code index:** `satellites code index`; prefer `code search`/`symbol` over
  Read/Grep (Grep wins for non-symbol text).
- **Always-context:** the SessionStart hook injects the resident set
  (`principles:always`) + index pointer and re-anchors it on each story
  `document_get` — pushed, don't fetch.
- **Work state:** `satellites work status` = live engagements;
  `story get <id>` / `ledger list <id>` = server status + history.

## On demand — do NOT preload

- **Index:** `satellites document index` lists every doc/principle (name, scope,
  `always`, headline; no bodies). Scan it, `document_get` only what's needed.
- **Principles:** non-resident layers via `document_list {tags:["principles:global"]}`
  (+ `principles:workspace` / `principles:project`); `principles:always` injected.
- **Reference:** `satellites_mcp_reference_dispatch`, `satellites_mcp_reference_documents`.
