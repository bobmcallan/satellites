---
name: satellites_client_install
tags: [kind:install-schema, v1]
target_install_path: ./.satellites/satellites
target_config_path: ./.satellites/satellites.toml
default_config:
  server_url: ""
  repo_path: .
  worktree_root: ./.satellites/worktree
  log_path: ./.satellites/logs
  branch_template: client-{task_id}-from-{base_sha}
  auth:
    token: ""
auth_bootstrap:
  kind: auth_login
  command: satellites auth login
  env_hint: SATELLITES_TOKEN
install:
  download_url: https://github.com/bobmcallan/satellites/releases/latest/download/satellites-{{cli_version}}-{{os}}-{{arch}}
  sha256_url:   https://github.com/bobmcallan/satellites/releases/latest/download/satellites-{{cli_version}}-{{os}}-{{arch}}.sha256
---
# satellites · install schema

The frontmatter is the operator-editable contract `document_get` returns
to MCP clients bootstrapping the CLI. Templated `install.*` fields
render against the server's system variables at retrieval time.

## Fields

| Frontmatter key                  | Meaning                                                                                                                                                 |
| -------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `target_install_path`            | Filesystem path the consumer writes the `satellites` CLI binary to (relative to the consumer project root, by convention).                            |
| `target_config_path`             | Filesystem path the consumer writes the canonical `satellites.toml` to.                                                                                 |
| `default_config.server_url`      | TOML `[server].url` — the satellites-server URL the CLI talks to. Empty defaults to the MCP-server-injected runtime URL.                              |
| `default_config.repo_path`       | TOML `[repo].path` — the consumer project's repo root.                                                                                                  |
| `default_config.worktree_root`   | TOML `[worktree].root` — where the daemon materialises per-task worktrees.                                                                              |
| `default_config.log_path`        | TOML `[logging].path` — CLI + per-task log destination.                                                                                                 |
| `default_config.branch_template` | TOML `[worktree].branch_template` — git branch name template the daemon uses when minting worktree branches.                                            |
| `default_config.auth.token`      | TOML `[auth].token` — the api-key the CLI presents on every server call. Empty in the schema; the bootstrap fills it from `auth_bootstrap.api_key`.   |
| `auth_bootstrap.kind`            | Auth bootstrap flow kind the operator runs after the binary lands. `auth_login` for first-time human bootstrap.                                         |
| `auth_bootstrap.command`         | The shell command the operator runs for `kind=auth_login`.                                                                                              |
| `auth_bootstrap.env_hint`        | Env-var name carrying the bearer after `auth login` mints it.                                                                                           |
| `install.download_url`           | Templated URL the agent fetches the CLI binary from. Renders against `{{cli_version}}`, `{{os}}`, `{{arch}}`.                                          |
| `install.sha256_url`             | Templated URL for the matching sha256 manifest. Same template variables as `download_url`.                                                              |

## Template variables

The `install.*` fields render against the server's system-variables
resolver. `{{cli_version}}` is the CLI release the server advertises
(ldflag-stamped at build); `{{os}}` and `{{arch}}` come from the
request body, falling back to the server's runtime defaults when
unsupplied.
