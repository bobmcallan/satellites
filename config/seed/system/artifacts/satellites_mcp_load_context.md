---
name: satellites_mcp_load_context
tags: [kind:mcp-startup, v1]
---
# satellites · MCP load context

When this MCP server is added to an agent session (Claude, Warp,
Codex, Gemini CLI, custom), the agent MUST run the following protocol
BEFORE running any project work. The MCP server returns this artifact
as its `initialize` instructions — treat it as load-bearing.

## Architecture in one paragraph

satellites exposes a verb registry. CLI, MCP, and HTTP all dispatch
through the same verbs — there is exactly **one implementation per
verb**, in the verb registry's `Invoke` function. The MCP transport is
intentionally light; after install/update, prefer `satellites exec
<verb>` (CLI) for heavy work and reserve MCP `tools/call` for
authoring + observation. Use `tools/list` to see every verb available.

## Step 1 — install or update the satellites CLI

Call the `satellites_init` tool with `current_version` = the version
string printed by the locally-installed CLI at `target_install_path`
(read via `<path> version`; empty string if the binary is absent).

Read `state` from the response:

| `state`            | Action                                                                 |
| ------------------ | ---------------------------------------------------------------------- |
| `install_required` | No local binary. Download (`install.download_url`), verify against `install.sha256_url`, `mkdir -p` the parent of `target_install_path`, write the binary, `chmod +x`. |
| `update_available` | Local binary is older than the server's published version. Same sequence; treat as overwrite. |
| `up_to_date`       | Skip the install steps; proceed.                                       |

After the binary is in place, materialise the TOML at
`target_config_path` from `default_config`. When
`auth_bootstrap.kind=ready`, substitute `auth_bootstrap.api_key` into
`[auth].token`. When `auth_bootstrap.kind=auth_login`, leave the token
empty and surface `auth_bootstrap.command` to the operator.

Verify by running `<target_install_path> version` — the CLI reads its
TOML at boot and prints the server it's bound to. If the printed
server doesn't match the MCP server's URL, the TOML write was wrong.

**Two auth surfaces, intentionally distinct:**
- This MCP session authenticates via OAuth. The consumer-side
  `.mcp.json` carries no bearer; the MCP SDK drives the AS discovery +
  DCR + authorize + token flow.
- The CLI authenticates with the api-key persisted in
  `.satellites/satellites.toml`'s `[auth].token`. The key
  `satellites_init` returns is FOR THAT TOML — never reuse it here.

## Step 2 — load workspace + project principles as context

Call `workspace_seed_get` (and `project_seed_get` if a `project_id` is
known) for the workspace + project the agent will operate against.
The returned markdown is the principle/convention context for
substrate work in that scope — read it BEFORE dispatching any domain
verbs. System defaults are inherited; workspace and project overrides
stack on top.

**Implementation status:** the seed verbs are not yet wired (tracked
under `epic:seed-system`, parent `sty_33554df9`, children
`sty_c19e64bf` + `sty_7f4b689c`). Until they land, agents should
attempt to read
`.satellites/seeds/<workspace_id>/workspace.md` and
`.satellites/seeds/<workspace_id>/<project_id>/project.md` from the
consumer repo's local filesystem if those paths exist. Missing seeds
mean "no scope-specific overrides — operate per substrate defaults".

## Step 3 — then, and only then, dispatch project verbs

`satellites story / project / workspace / exec / ...` calls happen
AFTER steps 1 + 2. An agent that skips step 1 may end up calling a
stale CLI; an agent that skips step 2 misses the operator's
scope-specific guidance. Both are visible-from-outside mistakes.

## Why this is the single MCP load instruction

This artifact (`satellites_mcp_load_context.md`) is the ONE document
an agent reads at MCP load time. Install schemas, principles, project
conventions — all are pulled lazily via the verbs named above, not
duplicated here. That keeps this file short, stable, and
authoritative: changes to install behaviour, workspace context, or
project principles do NOT require editing this file. Changes to the
load PROTOCOL itself do.
