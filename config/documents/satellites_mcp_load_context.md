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
and follow it — shell client: `satellites install` then `satellites auth`; write
the TOML; `project_match` the git remote for `project_id`. Then sync skills:
`satellites skill list` per scope, reconcile into `.claude/skills/<name>/SKILL.md`
per `document_get {name:"satellites_mcp_reference_skill_sync", scope:"system"}`.

## On demand — do NOT load eagerly

- **Principles** — `document_list {tags:["principles:global"]}` (and the
  `principles:workspace` / `principles:project` layers). Fetch only when a
  task's correctness depends on them, never at startup.
- **Reference** — `satellites_mcp_reference_dispatch` (CLI dispatch surface),
  `satellites_mcp_reference_documents` (upsert modes + list shapes),
  `satellites_mcp_reference_skill_sync` (skill reconciliation contract). Fetch
  the one whose contract you need.
