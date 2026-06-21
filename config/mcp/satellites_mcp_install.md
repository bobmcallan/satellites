---
name: satellites_mcp_install
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

`satellites auth` does NOT require a resolved `project_id`:
at cold start `project_id` is still empty (load-context Step 4 fills it via
`project_match`), so the key is minted against the caller's PERSONAL workspace
and `project match` binds `project_id` afterward — breaking the auth↔match
deadlock so a brand-new user reaches `status = db UP` with only the browser
approval. An explicit `--project` still scopes the mint to that project.

## Schema

```yaml
target_install_path:        ./.satellites/satellites   # in-repo dev / MCP-fallback path
target_install_path_global: ~/.local/bin/satellites    # shell clients: the shared binary on PATH (default)
target_config_path:  ./.satellites/satellites.toml
target_local_config_path: ./.satellites/satellites.local.toml   # gitignored per-user overlay (api_key, overrides) — mirrors .claude/settings.local.json
target_mcp_config_path: ./.mcp.json   # `satellites init` registers the MCP server here, from the same server_url
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
  # Shell-capable clients run the installer, which downloads + sha-verifies +
  # places the binary (--global → target_install_path_global; --local → the
  # in-repo target_install_path). MCP-only/manual clients use the URLs below.
  command:      satellites install
  cli_version:  "{{cli_version}}"
  download_url: https://github.com/bobmcallan/satellites/releases/latest/download/satellites-{{cli_version}}-{{os}}-{{arch}}
  sha256_url:   https://github.com/bobmcallan/satellites/releases/latest/download/satellites-{{cli_version}}-{{os}}-{{arch}}.sha256
```

## Fields

| Schema key                       | Meaning                                                                                                            |
| -------------------------------- | ------------------------------------------------------------------------------------------------------------------ |
| `target_install_path`            | In-repo binary path (`.satellites/satellites`) — the dev / MCP-fallback location; placed by `satellites install --local`. |
| `target_install_path_global`     | Shared global binary on PATH (`~/.local/bin/satellites`, the `claude` model) — the default for shell-capable clients; placed by `satellites install`. |
| `target_config_path`             | Where to write `satellites.toml` — the SHARED, committed, non-secret config (`server_url`, `project_id`).          |
| `target_local_config_path`       | Optional gitignored per-user overlay (`satellites.local.toml`), mirroring `.claude/settings.local.json`. Its set fields override `satellites.toml` (local wins). An `api_key` here authenticates the client INSTEAD of OAuth — precedence `SATELLITES_API_KEY` env > `satellites.local.toml` > OAuth credential store. The committed `satellites.toml` never holds the key. |
| `target_mcp_config_path`         | Where `satellites init` registers the MCP server (`.mcp.json`): `mcpServers.satellites = {type:http, url:<server_url>/mcp}`, derived from the SAME `server_url` as the toml so CLI and MCP can never point at different servers. Idempotent: identical preserved, divergent reconciled. |
| `default_config.server_url`      | TOML `server_url`. Server-rendered from the `{{server_url}}` system variable.                                      |
| `default_config.project_id`      | TOML `project_id`. Empty at bootstrap — load-context Step 4 fills it via `project_match`.                          |
| `default_config.repo_path`       | TOML `repo_path` — the consumer project's repo root.                                                               |
| `default_config.worktree_root`   | TOML `worktree_root` — where per-task worktrees materialise.                                                       |
| `default_config.log_path`        | TOML `log_path` — CLI + per-task log destination.                                                                  |
| `default_config.branch_template` | TOML `branch_template` — git branch name template. The inner `{task_id}` / `{base_sha}` are CLI-time substitutions, not server-side. |
| `auth_bootstrap.kind`            | Auth flow to run after install. `auth_login` = run `satellites auth` (browser login); the CLI stores an executor key in the user-level credential store (`$XDG_CONFIG_HOME/satellites/credentials.toml`, 0600). The api-key is NOT written to the TOML. |
| `auth_bootstrap.command`         | The shell command a shell-capable client runs — `satellites auth`.                                                |
| `auth_bootstrap.mcp_only`        | Fallback for MCP-only clients that cannot shell out: `kind: mcp_mint` dispatches `apikey_create` on the already-authenticated MCP session to mint a project-scoped key. |
| `install.command`                | The installer a shell-capable client runs — `satellites install` (places + sha-verifies the binary; `--global` default, `--local` for in-repo). |
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

A freshly-installed project authors its own reviewer-gated workflow — the
platform imposes none. No story is engaged first: drafting under
`.satellites/{workflows,principles,skills}` is ungated; the per-type reviewer at
`upload` is the control. Author these, then upload each (review-gated):

1. **Principles** — `.satellites/principles/*.md`, then `satellites principle
   upload`. The repo's beliefs / definition of "good" the gates read.
2. **Reviewer skills** — `.satellites/skills/*.md` (`kind:reviewer`), then
   `satellites skill upload`. One per transition you want gated; each judges a
   story against its rule and enacts the edge on accept.
3. **A workflow** — `.satellites/workflows/*.md` (`kind:workflow`), then
   `satellites workflow upsert`. States + ordered transitions, each naming the
   `reviewer_skill` that gates it; `applies_to` lists the story types it drives.

Each upload runs the per-type reviewer; a reject returns notes to revise and
re-run. A transition references a reviewer by name (the resolver normalises the
`satellites-` prefix). The loop runs once a workflow + its reviewers exist.
