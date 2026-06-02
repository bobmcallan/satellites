---
name: satellites_client_install
scope: system
tags: [kind:install-schema, v2]
---
# satellites · install schema

The fenced `yaml` block below is the machine-readable contract the
agent acts on. Server-side rendering fills every `\{\{name\}\}` with
the caller-or-platform value before the body is returned. Two fields
stay empty by design and are filled by named update steps in the
load-context:

- `default_config.project_id` — caller-resolved in load-context
  Step 4 (`project_match`).

The CLI no longer carries its api-key in `target_config_path` — the
TOML holds NON-secret config only. A shell-capable client provisions
its executor api-key by running `satellites auth` (browser login),
which stores the key in the user-level credential store
(`$XDG_CONFIG_HOME/satellites/credentials.toml`, mode 0600), keyed by
`server_url`. An MCP-only client that cannot shell out still mints
in-band via the `auth_bootstrap.mcp_only` fallback below.

## Schema

```yaml
target_install_path: ./.satellites/satellites
target_config_path:  ./.satellites/satellites.toml
default_config:
  server_url: "{{server_url}}"
  project_id: ""
  repo_path: .
  worktree_root: ./.satellites/worktree
  log_path: ./.satellites/logs
  branch_template: client-{task_id}-from-{base_sha}
auth_bootstrap:
  # Shell-capable clients: run `satellites auth` (browser login). The CLI
  # stores an executor key in the user-level credential store — never the TOML.
  kind: auth_login
  command: satellites auth
  # MCP-only clients that cannot shell out fall back to minting in-band:
  mcp_only:
    kind: mcp_mint
    verb: apikey_create
install:
  cli_version:  "{{cli_version}}"
  download_url: https://github.com/bobmcallan/satellites/releases/latest/download/satellites-{{cli_version}}-{{os}}-{{arch}}
  sha256_url:   https://github.com/bobmcallan/satellites/releases/latest/download/satellites-{{cli_version}}-{{os}}-{{arch}}.sha256
```

## Fields

| Schema key                       | Meaning                                                                                                            |
| -------------------------------- | ------------------------------------------------------------------------------------------------------------------ |
| `target_install_path`            | Where to write the CLI binary (relative to the consumer project root).                                             |
| `target_config_path`             | Where to write `satellites.toml`.                                                                                  |
| `default_config.server_url`      | TOML `server_url`. Server-rendered from the `{{server_url}}` system variable.                                      |
| `default_config.project_id`      | TOML `project_id`. Empty at bootstrap — load-context Step 4 fills it via `project_match`.                          |
| `default_config.repo_path`       | TOML `repo_path` — the consumer project's repo root.                                                               |
| `default_config.worktree_root`   | TOML `worktree_root` — where per-task worktrees materialise.                                                       |
| `default_config.log_path`        | TOML `log_path` — CLI + per-task log destination.                                                                  |
| `default_config.branch_template` | TOML `branch_template` — git branch name template. The inner `{task_id}` / `{base_sha}` are CLI-time substitutions, not server-side. |
| `auth_bootstrap.kind`            | Auth flow to run after install. `auth_login` = run `satellites auth` (browser login); the CLI stores an executor key in the user-level credential store (`$XDG_CONFIG_HOME/satellites/credentials.toml`, 0600). The api-key is NOT written to the TOML. |
| `auth_bootstrap.command`         | The shell command a shell-capable client runs — `satellites auth`.                                                |
| `auth_bootstrap.mcp_only`        | Fallback for MCP-only clients that cannot shell out: `kind: mcp_mint` dispatches `apikey_create` on the already-authenticated MCP session to mint a project-scoped key. |
| `install.cli_version`            | The CLI release this schema points at. Server-rendered from `{{cli_version}}`.                                     |
| `install.download_url`           | URL to fetch the CLI binary from. Server-rendered with `{{cli_version}}`, `{{os}}`, `{{arch}}`.                    |
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

## Process setup

A freshly-installed project defines its own reviewer-gated workflow — the
platform imposes none. Read `document_get {name:"project-setup",
scope:"system"}` and follow it: it teaches the fixed structure (reviewer-only
transitions, the story-as-contract) and walks the agent through defining the
repo's states, transitions, and per-gate criteria from the admin's
requirements and the repo's reality, then authoring the project-scoped
workflow + gate skills. The loop runs once those exist.
