---
name: satellites_mcp_load_context
tags: [kind:mcp-startup, v1]
---
# satellites · MCP load context

Complete these steps before any other satellites tool call or CLI
invocation.

## Step 1 — fetch the install schema

Call `document_get`:

```json
{
  "name": "satellites_client_install",
  "scope": "system",
  "os": "<linux|darwin|windows>",
  "arch": "<amd64|arm64>",
  "current_version": "<output of `<target_install_path> version` or empty>"
}
```

The response's `rendered_body` is a YAML frontmatter block. Parse it
for these fields:

| Field                              | Meaning                                                                                                                       |
| ---------------------------------- | ----------------------------------------------------------------------------------------------------------------------------- |
| `target_install_path`              | Filesystem path for the `satellites` CLI binary.                                                                              |
| `target_config_path`               | Filesystem path for the canonical `satellites.toml`.                                                                          |
| `default_config.*`                 | The TOML's expected contents (server_url, repo_path, worktree_root, log_path, branch_template, auth.token).                  |
| `install.download_url`             | URL to fetch the CLI binary from. Already rendered against `cli_version`, `os`, `arch`.                                       |
| `install.sha256_url`               | Matching sha256 manifest URL.                                                                                                 |
| `auth_bootstrap.kind`              | Bootstrap-auth flow kind. `auth_login` means the operator must run `auth_bootstrap.command` to mint an api-key.               |

## Step 2 — install the binary if needed

Compare `current_version` (passed in) to the value embedded in
`install.download_url` (the URL contains the version segment between
the `satellites-` prefix and the `-<os>` suffix).

| Caller state                                                  | Action                                                                                                                  |
| ------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------- |
| Binary absent (empty `current_version`).                      | Download `install.download_url`, verify against `install.sha256_url`, `mkdir -p` the parent of `target_install_path`, write the binary, `chmod +x`. |
| Local version differs from the URL's version segment.         | Same download sequence; overwrite in place.                                                                             |
| Local version matches.                                        | Skip.                                                                                                                   |

## Step 3 — write the TOML

Write `target_config_path` from the schema's `default_config`. When
`auth_bootstrap.kind=ready`, substitute `auth_bootstrap.api_key` into
the TOML's `[auth].token`. When `auth_bootstrap.kind=auth_login`,
leave `[auth].token` empty, print `auth_bootstrap.command` to the
operator, and stop.

Verify by running `<target_install_path> version`.

**Two auth surfaces, intentionally distinct:**

- The MCP session authenticates via OAuth. The client-side `.mcp.json`
  carries no bearer; the MCP SDK drives discovery + DCR + authorize +
  token.
- The CLI authenticates with the api-key persisted in
  `target_config_path`'s `[auth].token`. The api-key returned in the
  bootstrap is for that TOML — do not reuse it on the MCP session.

## Step 4 — dispatch project verbs via the CLI

`tools/list` on this MCP server returns `document_get` only. Every
other verb (story, project, workspace, document, variable, exec, …)
is reachable via `<target_install_path> exec <verb>` once Step 3 is
complete. Use `<target_install_path> help` to list local subcommands.
