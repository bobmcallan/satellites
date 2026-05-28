---
name: satellites_mcp_load_context
scope: system
tags: [kind:mcp-startup, v2]
---
# satellites · bootstrap

Complete these before any other tool call.

## 1. Translate operator prefixes

| Prefix | Resolve via |
|---|---|
| `document:<scope>/<name>` | `document_get` |
| `project:<id>` | `project_get` |
| `story:<id>` | `document_get` |
| `epic:<slug>` | `document_list` with `tags:["epic:<slug>"]` |
| `variable:<scope>/<name>` | `variable_get` |

## 2. Bootstrap the client

Call `document_get {name:"satellites_client_install", scope:"system", os, arch, current_version}`. Install or update the CLI per the returned schema. Write the TOML. Mint the api-key via `apikey_create`. Resolve `project_id` via `project_match` on the consumer git remote.

## 3. Load principles

Call `document_list` three times to fetch every layer that applies:

- `{scope:"system", tags:["principles:global"]}`
- `{scope:"workspace", tags:["principles:workspace"], workspace_id:<wksp_id>}`
- `{scope:"project", tags:["principles:project"], project_id:<proj_id>}`

Read each body. Re-deliver on every read verb that names a matching scope — refresh per call, do not cache.

## 4. Sync Claude Code skills

Run `satellites skill list` once per scope that applies — system; workspace with `--scope workspace --workspace <wksp_id>`; project with `--scope project --workspace <wksp_id> --project <proj_id>`. For each returned row:

- Read `.claude/skills/<name>/SKILL.md`. Missing file, missing `version:` in frontmatter, or `version` below the substrate `latest_version` → outdated.
- For each outdated row, call `satellites skill get <name>` (same scope flags) and write the body to `.claude/skills/<name>/SKILL.md` with frontmatter `name`, `description`, and `version` set to the synced `latest_version`.
- Pull-only. Never delete a local skill file — operators own `.claude/skills/`, and a local skill with no substrate counterpart is left alone.

This step covers Claude Code skills only — substrate rows with `type:"skill"`. Workflow specs (`feature-workflow.md` and siblings) are `type:"document"` rows read by the server's workflow-skill spec parser; they are not Claude Code skills and must not be written into `.claude/skills/`. `skill list` returns `type:"skill"` rows only, so the source enforces the line; the naming distinction prevents the category error if a reader hand-grafts a workflow spec into the skills tree.

Skip this step and skills drift between sessions. Reviewer behaviour then varies by whichever client ran last — the failure mode this contract exists to prevent.

## 5. Fetch reference docs on demand

- `document_get {name:"satellites_mcp_reference_dispatch", scope:"system"}` — CLI dispatch surface, typed subcommands, story actions.
- `document_get {name:"satellites_mcp_reference_documents", scope:"system"}` — upsert modes, list filter shape, MCP-only client surface.
