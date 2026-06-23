---
name: satellites_mcp_reference_documents
scope: system
tags: [kind:mcp-reference]
---
# satellites · reference: documents and stories

Stories and documents are the same substrate kind — one row in the documents table, distinguished by `type`. `document_*` verbs cover both.

## Upsert modes (document_upsert)

The verb chooses its mode by inspecting the request:

| Shape | Mode |
|---|---|
| `{"type":"story", "project_id":"<proj_id>", "name":"<title>", "body":"<md>", "tags":["epic:<slug>"]}` | Story create. Mints a fresh `sty_<id>` and inserts the row. |
| `{"id":"<sty_id>", "status":"in_progress", "tags":["<tag>"], "body":"<md>"}` | Story update. Patches metadata; body change appends a new version. Pointer fields (`tags`, `status`, `priority`, `category`, `parent_id`, `acceptance_criteria`) ignore omitted keys. |
| `{"type":"document", "scope":"project", "workspace_id":"<wksp_id>", "project_id":"<proj_id>", "name":"<doc-name>", "body":"<md>"}` | Document content upsert (`type:document`). Key-addressed; first call inserts, subsequent calls append a new version. Writable over MCP (scope-bounded by membership) **or** the CLI. Skill / principle / workflow content is CLI-only (see below). |

## List filter shape (document_list)

```json
{
  "type": "story",
  "scope": "project",
  "workspace_id": "<wksp_id>",
  "project_id": "<proj_id>",
  "tags": ["epic:<slug>", "area:<topic>"],
  "status": "in_progress",
  "name_prefix": "<prefix>",
  "limit": 50,
  "cursor": ""
}
```

Response: `{"items": [<document>...], "next_cursor": "<cursor>"}`. Pass `next_cursor` back as `cursor` on the next call. Ordering is `created_at DESC, id DESC` — cursors stay stable under inserts.

## MCP surface — read + setup + document/story writes

Hosted assistants that cannot shell out use the MCP server directly; the Bearer credential on the session authorises each call — no TOML, no installed binary. Reachable over MCP: `document_get` / `document_list`, `project_*`, `apikey_*`, and `document_upsert` / `document_delete` for **stories and `type:document` content** (each scope-bounded by the caller's membership).

Behaviour-kind content — **skill / principle / workflow** — is NOT on the MCP surface: it runs through the CLI (`satellites {skill,principle} upload`, `satellites workflow upsert`) so the per-type reviewer runs first. **Tasks** are created with the CLI (`satellites task`), and **changelog** is server-managed. An attempt to upsert any of these over MCP is refused with a pointer to the CLI.

`project_id` is required on `document_upsert` for `type:"story"`. Use `project_match` to resolve it from the operator's git remote when the operator names a repo rather than an id.
