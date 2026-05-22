---
name: satellites_client_install
tags: [kind:install-schema, v1]
target_install_path: ./.satellites/satellites
target_config_path: ./.satellites/satellites.toml
default_config:
  server_url: ""
  project_id: ""
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
  cli_version:  "{{cli_version}}"
  download_url: https://github.com/bobmcallan/satellites/releases/latest/download/satellites-{{cli_version}}-{{os}}-{{arch}}
  sha256_url:   https://github.com/bobmcallan/satellites/releases/latest/download/satellites-{{cli_version}}-{{os}}-{{arch}}.sha256
---
# satellites · install schema

## Fields

| Frontmatter key                  | Meaning                                                                                                            |
| -------------------------------- | ------------------------------------------------------------------------------------------------------------------ |
| `target_install_path`            | Write the CLI binary to this path (relative to the consumer project root).                                         |
| `target_config_path`             | Write `satellites.toml` to this path.                                                                              |
| `default_config.server_url`      | TOML `[server].url`. Empty means: use the URL of the MCP server you fetched this document from.                    |
| `default_config.project_id`      | TOML `[project].id`. Resolve via `project_match` (see load-context), then write the returned `project_id` here.    |
| `default_config.repo_path`       | TOML `[repo].path` — the consumer project's repo root.                                                             |
| `default_config.worktree_root`   | TOML `[worktree].root` — where per-task worktrees materialise.                                                     |
| `default_config.log_path`        | TOML `[logging].path` — CLI + per-task log destination.                                                            |
| `default_config.branch_template` | TOML `[worktree].branch_template` — git branch name template.                                                      |
| `default_config.auth.token`      | TOML `[auth].token` — the api-key the CLI presents on every server call. Empty until `auth_bootstrap` completes.   |
| `auth_bootstrap.kind`            | Auth flow to run after install. `auth_login` = run `auth_bootstrap.command` and read the bearer from its output.  |
| `auth_bootstrap.command`         | Shell command the operator runs for `kind=auth_login`.                                                             |
| `auth_bootstrap.env_hint`        | Env-var name carrying the bearer after `auth login` mints it.                                                      |
| `install.cli_version`            | The CLI release this schema points at. Pre-rendered.                                                               |
| `install.download_url`           | URL to fetch the CLI binary from. Pre-rendered.                                                                    |
| `install.sha256_url`             | Matching sha256 manifest URL. Pre-rendered.                                                                        |

All `\{\{name\}\}` values in the frontmatter are substituted
server-side before the document is returned. The body you receive
carries concrete strings, not templates — do not re-substitute.
