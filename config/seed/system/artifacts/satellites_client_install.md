---
name: satellites_client_install
tags: [kind:install-schema, v2]
target_install_path: ./.satellites/satellites
target_config_path: ./.satellites/satellites.toml
default_config:
  server_url: "{{server_url}}"
  project_id: ""
  repo_path: .
  worktree_root: ./.satellites/worktree
  log_path: ./.satellites/logs
  branch_template: client-{task_id}-from-{base_sha}
  auth:
    token: ""
auth_bootstrap:
  kind: mcp_mint
  verb: apikey_create
  env_hint: SATELLITES_TOKEN
install:
  cli_version:  "{{cli_version}}"
  download_url: https://github.com/bobmcallan/satellites/releases/latest/download/satellites-{{cli_version}}-{{os}}-{{arch}}
  sha256_url:   https://github.com/bobmcallan/satellites/releases/latest/download/satellites-{{cli_version}}-{{os}}-{{arch}}.sha256
---
# satellites · install schema

`default_config` is the literal TOML you — the agent — write to
`target_config_path`. After server-side rendering, every key has a
usable default. Two fields stay empty by design and are filled by
named update steps in the load-context:

- `project_id` — caller-resolved in load-context Step 4 (`project_match`).
- `[auth].token` — filled by the `auth_bootstrap` flow.

The CLI is read-only against `target_config_path`. The agent is the
sole writer of that file (load-context Step 3).

## Fields

| Frontmatter key                  | Meaning                                                                                                            |
| -------------------------------- | ------------------------------------------------------------------------------------------------------------------ |
| `target_install_path`            | Where to write the CLI binary (relative to the consumer project root).                                             |
| `target_config_path`             | Where to write `satellites.toml`.                                                                                  |
| `default_config.server_url`      | TOML `server_url`. Server-rendered from the `{{server_url}}` system variable.                                      |
| `default_config.project_id`      | TOML `project_id`. Empty at bootstrap — load-context Step 4 fills it via `project_match`.                          |
| `default_config.repo_path`       | TOML `repo_path` — the consumer project's repo root.                                                               |
| `default_config.worktree_root`   | TOML `worktree_root` — where per-task worktrees materialise.                                                       |
| `default_config.log_path`        | TOML `log_path` — CLI + per-task log destination.                                                                  |
| `default_config.branch_template` | TOML `branch_template` — git branch name template. The inner `{task_id}` / `{base_sha}` are CLI-time substitutions, not server-side. |
| `default_config.auth.token`      | TOML `[auth].token` — the api-key the CLI presents on every server call. Empty until `auth_bootstrap` completes.   |
| `auth_bootstrap.kind`            | Auth flow to run after install. `mcp_mint` = MCP caller dispatches the named verb on its already-authenticated session to mint a project-scoped api-key; no shell-out required. |
| `auth_bootstrap.verb`            | Substrate verb dispatched under `kind=mcp_mint` — `apikey_create`. See the load-context bootstrap step.            |
| `auth_bootstrap.env_hint`        | Env-var name carrying the bearer when callers prefer to inject it via environment rather than the TOML.            |
| `install.cli_version`            | The CLI release this schema points at. Server-rendered from `{{cli_version}}`.                                     |
| `install.download_url`           | URL to fetch the CLI binary from. Server-rendered.                                                                 |
| `install.sha256_url`             | Matching sha256 manifest URL. Server-rendered.                                                                     |

## Variable substitution

`\{\{name\}\}` placeholders in this document are rendered server-side
from the variable cascade (system → workspace → project; system wins
for platform-owned names). The body you receive carries concrete
strings — do not re-substitute.

If a `\{\{name\}\}` literal still appears in the rendered body, that
variable is unset in the cascade and appears on the response's
`unresolved_vars`. Repair by setting it (`variable_set` for
workspace/project scope; system variables are platform-owned and
cannot be set via the verb) and re-fetch the document. Do not
invent a value in agent code — a missing variable is a configuration
gap to fill in the bootstrap process, not a runtime fallback.
