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

## Prefixes (resolve when one appears)

`document:<scope>/<name>` → `document_get` · `project:<id>` → `project_get` ·
`story:<id>` → `document_get` · `epic:<slug>` → `document_list {tags:["epic:<slug>"]}` ·
`variable:<scope>/<name>` → `variable_get`

## First-time setup (once per repo)

`document_get {name:"satellites_client_install", scope:"system", os, arch, current_version}`
and follow it — `satellites install`, `satellites auth`, write the TOML,
`project_match` the git remote for `project_id`.

## Session start

- **Skills:** `satellites skill sync` — one pull-only pass materialises every
  scope (system/workspace/project) into `.claude/skills/<name>/SKILL.md` by
  identity stamp (never clobbering operator-authored skills). The client's job —
  do **not** hand-reconcile by listing + writing files.
- **Code index:** for an indexable repo, `satellites code index` (incremental),
  then prefer `satellites code search <q>` / `satellites code symbol <name>` over
  Read/Grep for code discovery — exact `file:line` + slices, far fewer tokens.
  See the `satellites-code-search` skill (Grep still wins for non-symbol text).

## On demand — do NOT load eagerly

- **Principles** — `document_list {tags:["principles:global"]}` (+ the
  `principles:workspace` / `principles:project` layers). Fetch only when a task's
  correctness depends on them, never at startup.
- **Reference** — `satellites_mcp_reference_dispatch` (CLI dispatch surface),
  `satellites_mcp_reference_documents` (upsert modes + list shapes).
