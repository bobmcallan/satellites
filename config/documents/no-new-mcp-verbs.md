---
name: no-new-mcp-verbs
type: document
scope: system
tags: [principles:global]
---
# No new MCP verbs

Compose the existing MCP verbs; do not extend the surface. Every MCP behaviour
is a contract every client must understand, so the surface stays narrow.

- New substrate kinds are `type:"<kind>"` rows accessed via the existing
  `document_*` verbs — not new `<kind>_get`/`<kind>_list`/`<kind>_upsert` verbs.
- Client-side ergonomics (listings, materialisation, sync, comparison) ship as
  `satellites` CLI subcommands, not MCP verbs — especially when only one
  transport wants them.
- Sanctioned exceptions are `project_match`, `apikey_create`, and `system_status`
  (read-only health/identity with no `document_*` equivalent). The exception
  list is named here; widening it is itself a reviewed decision.
