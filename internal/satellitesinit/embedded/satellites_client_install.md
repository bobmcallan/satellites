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
auth_bootstrap:
  kind: auth_login
  command: satellites auth login
  env_hint: SATELLITES_TOKEN
---
# satellites_init · install schema

This artifact is the **canonical, operator-editable source of truth**
for the install schema the `satellites_init` verb returns. The schema
describes where the consumer project drops the `satellites` CLI binary,
where its TOML config lives, and the bootstrap auth flow the operator
runs after the binary lands.

Runtime values come from the **frontmatter** above, not the body. The
file is embedded into the satellites-server binary at build time; edit
the frontmatter and rebuild to change a default. (Once the document
store lands in a later PR, this same file becomes the seed source for a
DB-stored artifact row — same shape on the wire either way.)

## Fields

| Frontmatter key                       | Meaning                                                                                                                         |
| ------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------- |
| `target_install_path`                 | Filesystem path the consumer writes the `satellites` CLI binary to (relative to the consumer project root, by convention).     |
| `target_config_path`                  | Filesystem path the consumer writes the canonical `satellites.toml` to.                                                         |
| `default_config.server_url`           | TOML `[server].url` — the satellites-server URL the CLI talks to. Empty defaults to the MCP-server-injected runtime URL.        |
| `default_config.repo_path`            | TOML `[repo].path` — the consumer project's repo root.                                                                          |
| `default_config.worktree_root`        | TOML `[worktree].root` — where the daemon materialises per-task worktrees.                                                      |
| `default_config.log_path`             | TOML `[logging].path` — CLI + per-task log destination.                                                                         |
| `default_config.branch_template`      | TOML `[worktree].branch_template` — git branch name template the daemon uses when minting worktree branches.                    |
| `auth_bootstrap.kind`                 | Auth bootstrap flow kind the operator runs after the binary lands. `auth_login` for first-time human bootstrap.                 |
| `auth_bootstrap.command`              | The shell command the operator runs for `kind=auth_login`.                                                                      |
| `auth_bootstrap.env_hint`             | Env-var name carrying the bearer after `auth login` mints it.                                                                   |

## Release-pipeline-derived fields (NOT in this artifact)

The verb also emits `install.{download_url, sha256_url, version, os,
arch}`. Those source from the satellites-server's own build-time
version stamp + GitHub's release URL pattern — never from this
artifact. Editing this file does not affect which binary version
satellites_init recommends; that comes from the server binary's
`-ldflags -X verb.Version=...` injection at release time.
