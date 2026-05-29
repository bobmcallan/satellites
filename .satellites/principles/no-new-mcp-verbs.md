---
tags: [principles:project]
---
# No new MCP verbs

The MCP surface is intentionally narrow and stays that way. Every
behaviour MCP exposes is a contract every client — Claude, Warp,
Codex, the satellites CLI — must understand. New work composes the
existing verbs; it does not extend the surface.

## What this rules in

- New substrate kinds are added as `type:"<kind>"` rows accessed via
  the existing `document_get` / `document_list` / `document_upsert` /
  `document_delete` verbs.
- Client-side ergonomics — listings, materialisation, comparison
  against a local cache, sync — ship as `satellites` CLI subcommands.
- Bootstrap verbs (`project_match`, `apikey_create`) are the lone
  exception. They exist because no client can call MCP without them.

## What this rules out

- New `<kind>_get` / `<kind>_list` / `<kind>_upsert` MCP verbs when
  `type:"<kind>"` on the document surface already covers them.
- Adding MCP verbs for operations only one transport (typically the
  CLI) actually wants.

## Enforcement

Reviewer-side. The `exposedVerbs` list in
`internal/mcpserver/server.go` is the surface; additions go through
review against this principle.
