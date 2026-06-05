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
the TOML; `project_match` the git remote for `project_id`.

## Skill sync (session start)

Materialise the substrate's skills with the client: **`satellites skill sync`**.
One pull-only pass reconciles every scope the repo can see — system + workspace +
project — into `.claude/skills/<name>/SKILL.md` by identity stamp (install /
update / skip; never clobbering an operator-edited or operator-authored skill).
This is the client's job — do **not** hand-reconcile by listing + writing files.

## On demand — do NOT load eagerly

- **Principles** — `document_list {tags:["principles:global"]}` (and the
  `principles:workspace` / `principles:project` layers). Fetch only when a
  task's correctness depends on them, never at startup.
- **Reference** — `satellites_mcp_reference_dispatch` (CLI dispatch surface),
  `satellites_mcp_reference_documents` (upsert modes + list shapes). Fetch the
  one whose contract you need.
