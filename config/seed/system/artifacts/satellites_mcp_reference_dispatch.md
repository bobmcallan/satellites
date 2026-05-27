---
name: satellites_mcp_reference_dispatch
tags: [kind:mcp-reference]
---
# satellites · reference: CLI dispatch

Every MCP verb is dispatchable from the CLI: `satellites exec <verb> --json '<args>'`. Behaviour is byte-identical to the MCP call.

Array fields (`tags`, `versions`) must be passed as real JSON arrays — never the stringified form. The dispatcher rejects stringified arrays with `bad_request`.

## Typed CLI subcommands

| Group | Use |
|---|---|
| `project match --remote <url>` | Resolve a git remote to a `project_id`. |
| `exec <verb> --json '<args>'` | Direct verb dispatch — JSON in, JSON out. |
| `version` | Print the CLI's stamped version. |

## Story shortcuts

Stories are documents with `type:"story"`. There is no `satellites story <op>` subcommand and no `story_*` verb.

| Action | Call |
|---|---|
| Create | `satellites exec document_upsert --json '{"type":"story","project_id":"<proj_id>","name":"<title>","body":"<md>","tags":["epic:<slug>"]}'` |
| Read | `satellites exec document_get --json '{"id":"<sty_id>"}'` |
| Update | `satellites exec document_upsert --json '{"id":"<sty_id>","status":"in_progress","tags":["<tag>"]}'` |
| Delete | `satellites exec document_delete --json '{"id":"<sty_id>"}'` |
| List | `satellites exec document_list --json '{"type":"story","project_id":"<proj_id>"}'` |

## project_id fallback

When `--project-id` is omitted on an operational verb, the CLI falls back to the TOML's `project_id`. If neither is set, the CLI returns `project_id not defined`. Treat that error as bootstrap drift: call `project_match` on the consumer git remote, write the result into the TOML, retry.
