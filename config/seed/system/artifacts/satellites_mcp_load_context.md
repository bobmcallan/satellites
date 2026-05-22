---
name: satellites_mcp_load_context
tags: [kind:mcp-startup, v1]
---
# satellites · MCP load context

Complete the steps below before any other satellites tool call or CLI
invocation. Treat each step as a contract: the next step assumes the
previous one ran.

## Step 1 — fetch the install schema

Call `document_get`:

```json
{
  "name": "satellites_client_install",
  "scope": "system",
  "os": "<linux|darwin|windows>",
  "arch": "<amd64|arm64>",
  "current_version": "<output of `<target_install_path> version`, or empty when not installed>"
}
```

Parse the response's `rendered_body` as a YAML frontmatter block.
Read these fields:

| Field                       | Use                                                                                       |
| --------------------------- | ----------------------------------------------------------------------------------------- |
| `target_install_path`       | Where to write the CLI binary (relative to the consumer project root).                    |
| `target_config_path`        | Where to write `satellites.toml`.                                                         |
| `default_config.*`          | TOML contents (server_url, project_id, repo_path, worktree_root, log_path, branch_template, auth.token). |
| `install.cli_version`       | The CLI release this schema points at.                                                    |
| `install.download_url`      | Where to fetch the CLI binary.                                                            |
| `install.sha256_url`        | Matching sha256 manifest URL.                                                             |
| `auth_bootstrap.kind`       | Auth flow to run. `auth_login` = operator runs `auth_bootstrap.command` to mint a token. |

## Step 2 — install or update the CLI

Compare the request's `current_version` against `install.cli_version`:

| Caller state                                           | Action                                                                                                                                            |
| ------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------- |
| `current_version` empty.                               | Download `install.download_url`, verify against `install.sha256_url`, `mkdir -p` the parent of `target_install_path`, write the binary, `chmod +x`. |
| `current_version` differs from `install.cli_version`.  | Same download sequence; overwrite in place.                                                                                                       |
| `current_version` matches `install.cli_version`.       | Skip.                                                                                                                                             |

## Step 3 — write the TOML

Write `target_config_path` from the schema's `default_config`. When
`auth_bootstrap.kind=auth_login`, leave `[auth].token` empty, print
`auth_bootstrap.command` to the operator, and stop until the operator
has run it. The minted token belongs in the TOML's `[auth].token` —
do not reuse it on the MCP session, which authenticates separately.

Verify by running `<target_install_path> version`.

## Step 4 — resolve and persist `project_id`

Operational CLI verbs require a `project_id`. Resolve it from the
consumer repo's git remote and persist it into the TOML:

1. Read the consumer repo's git remote (`git remote get-url origin`
   or equivalent). Any of these forms work:
   - `https://<host>/<owner>/<repo>[.git]`
   - `git@<host>:<owner>/<repo>[.git]`
   - `ssh://git@<host>/<owner>/<repo>`
   - `<scope>:<host>/<owner>/<repo>`
   - `<host>/<owner>/<repo>`
2. Call `project_match` with `{"git_url": "<remote>"}`. Response:
   `{"project_id": "<id>", "matched_url": "<canonical>", ...}`.
3. Write the returned `project_id` into the TOML's `project_id` field.

If `project_match` returns `not_found`, surface the error to the
operator — the project must be created (out of scope for bootstrap).

## Step 5 — dispatch every other verb via the CLI

`tools/list` on this MCP server returns `document_get` and
`project_match` only. Both are bootstrap surfaces. Every operational
verb (story, document, variable, workspace, exec, …) reaches the
substrate through the CLI you installed in Step 2.

Use `<target_install_path> --help` for the command tree. Typed
subcommands cover the high-traffic verbs:

| Group         | Use                                                                |
| ------------- | ------------------------------------------------------------------ |
| `init`        | Re-write the TOML (accepts `--project-id`, `--api-key`, …).        |
| `project`     | `match --remote <url>` resolves a git remote to a `project_id`.    |
| `story`       | `create` / `list` / `get` / `update`. `list` accepts `--tag`.      |
| `exec <verb>` | Direct verb dispatch for every other verb. JSON in, JSON out.      |
| `version`     | Prints the CLI's stamped version.                                  |

When `--project-id` is omitted on an operational verb, the CLI falls
back to the TOML's `project_id`. If neither is set, the CLI returns
`error project_id not defined`.

## Reference prefixes

Operator messages routinely name substrate entities by short prefix.
Translate them as follows:

| Prefix                    | Resolves via                                                                                       |
| ------------------------- | -------------------------------------------------------------------------------------------------- |
| `document:<scope>/<name>` | `<cli> exec document_get '{"scope":"<scope>","name":"<name>"}'`. Also reachable as the MCP tool.   |
| `project:<id>`            | `<cli> exec project_get '{"id":"<id>"}'`.                                                          |
| `story:<id>`              | `<cli> story get --id <id>`.                                                                       |
| `epic:<slug>`             | `<cli> story list --tag epic:<slug>` (filter is AND across repeated `--tag`).                      |
| `variable:<scope>/<name>` | `<cli> exec variable_get '{"scope":"<scope>","name":"<name>"}'`.                                   |

`<scope>` is `system`, `workspace`, or `project`.
