---
name: satellites_mcp_load_context
tags: [kind:mcp-startup, v1]
---
# satellites · MCP load context

Complete these steps before any other satellites tool call or CLI invocation.

## Step 1 — install or refresh the satellites CLI

Call `satellites_init` with `current_version` = the version string
printed by the locally-installed CLI at `target_install_path` (read
via `<path> version`). Pass an empty string if the binary is absent.

Read `state` from the response:

| `state`            | Action                                                                 |
| ------------------ | ---------------------------------------------------------------------- |
| `install_required` | No local binary. Download `install.download_url`, verify against `install.sha256_url`, `mkdir -p` the parent of `target_install_path`, write the binary, `chmod +x`. |
| `update_available` | Local binary is older than the server's published version. Same sequence; overwrite in place. |
| `up_to_date`       | Skip the install steps.                                                |

After the binary is in place, write the TOML at `target_config_path`
from the response's `default_config` field. When
`auth_bootstrap.kind=ready`, substitute `auth_bootstrap.api_key` into
the TOML's `[auth].token`. When `auth_bootstrap.kind=auth_login`,
leave `[auth].token` empty, print `auth_bootstrap.command` to the
operator, and stop.

Verify by running `<target_install_path> version`. If the CLI's
output doesn't name the MCP server's URL, the TOML write was wrong.

**Two auth surfaces, intentionally distinct:**
- The MCP session authenticates via OAuth. The client-side `.mcp.json`
  carries no bearer; the MCP SDK drives discovery + DCR + authorize +
  token.
- The CLI authenticates with the api-key persisted in
  `target_config_path`'s `[auth].token`. The key returned by
  `satellites_init` is for that TOML — do not reuse it on the MCP
  session.

## Step 2 — dispatch project verbs via the CLI

`tools/list` on this MCP server returns `satellites_init` only. Every
other verb (story, project, workspace, exec, …) is reachable via
`<target_install_path> exec <verb>` once Step 1 is complete.
