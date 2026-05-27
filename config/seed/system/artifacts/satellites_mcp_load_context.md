---
name: satellites_mcp_load_context
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

## 4. Fetch reference docs on demand

- `document_get {name:"satellites_mcp_reference_dispatch", scope:"system"}` — CLI dispatch surface, typed subcommands, story actions.
- `document_get {name:"satellites_mcp_reference_documents", scope:"system"}` — upsert modes, list filter shape, MCP-only client surface.
