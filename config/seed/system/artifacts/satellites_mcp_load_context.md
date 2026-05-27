---
name: satellites_mcp_load_context
tags: [kind:mcp-startup, v1]
---
# satellites · MCP load context

Complete the steps below before any other satellites tool call or CLI
invocation. Treat each step as a contract: the next step assumes the
previous one ran.

## Step 0 — translate operator references

Operator messages routinely name substrate entities by short prefix.
Before any other tool call, filesystem search, or response, scan the
operator's latest message for these prefixes. Each match MUST be
resolved through the CLI listed below — never through `grep`, file
read, or guess.

| Prefix                    | Resolves via                                                                                                  |
| ------------------------- | ------------------------------------------------------------------------------------------------------------- |
| `document:<scope>/<name>` | `<cli> exec document_get '{"scope":"<scope>","name":"<name>"}'`. Also reachable as the `document_get` MCP tool. |
| `project:<id>`            | `<cli> exec project_get '{"id":"<id>"}'`.                                                                     |
| `story:<id>`              | `<cli> story get --id <id>`.                                                                                  |
| `epic:<slug>`             | `<cli> story list --tag epic:<slug>` (filter is AND across repeated `--tag`).                                 |
| `variable:<scope>/<name>` | `<cli> exec variable_get '{"scope":"<scope>","name":"<name>"}'`.                                              |

`<scope>` is `system`, `workspace`, or `project`. `<cli>` is the
binary you installed in Step 2 (`target_install_path` from the install
schema). If the CLI is not yet installed, complete Steps 1–4 first
and then resolve the references; do not act on a prefix before the
CLI can answer.

If the operator's message contains no prefix from this table, skip to
Step 1.

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
| `auth_bootstrap.kind`       | Auth flow to run. `mcp_mint` = dispatch `auth_bootstrap.verb` on the current MCP session to mint a token in-band. |
| `auth_bootstrap.verb`       | Verb to dispatch under `kind=mcp_mint`. Currently `apikey_create`. |

## Step 2 — install or update the CLI

Compare the request's `current_version` against `install.cli_version`:

| Caller state                                           | Action                                                                                                                                            |
| ------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------- |
| `current_version` empty.                               | Download `install.download_url`, verify against `install.sha256_url`, `mkdir -p` the parent of `target_install_path`, write the binary, `chmod +x`. |
| `current_version` differs from `install.cli_version`.  | Same download sequence; overwrite in place.                                                                                                       |
| `current_version` matches `install.cli_version`.       | Skip.                                                                                                                                             |

## Step 3 — write the TOML

You — the agent — are the sole writer of `target_config_path`. The
CLI reads this file; it never creates or mutates it. Copy the
schema's `default_config` block verbatim into the TOML at
`target_config_path`. Then run the explicit update steps below for
fields the spec defers to caller-provided values: `project_id`
(Step 4) and `auth.token` (this step's auth bootstrap).

When `auth_bootstrap.kind=mcp_mint`, dispatch `auth_bootstrap.verb`
(currently `apikey_create`) on the current MCP session — the session
is already authenticated by the operator's bearer credential, and the
verb inherits that identity. Request shape:

```json
{"workspace_id":"wksp_…","project_id":"proj_…","agent_name":"<slug>","scopes":[]}
```

Response:

```json
{"apikey":"sk_…","key_id":"apk_…","workspace_id":"…","project_id":"…","agent_name":"…","created_at":"…"}
```

Write the response's `apikey` value into the TOML's `[auth].token`.
That key is project-scoped under the caller's identity; the MCP
session's own bearer is separate and unaffected. If the operator's
identity does not have membership on the target workspace, the verb
returns a `forbidden` error — surface it; the operator must be added
to the workspace before bootstrap can finish.

For CLI-only or operator-driven flows that need to revoke or rotate
the minted token, use `apikey_list` to enumerate the caller's keys
and `apikey_revoke` with the returned `key_id`.

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

`tools/list` on this MCP server returns thirteen verbs:
`document_get`, `document_list`, `document_count`, `document_upsert`,
`document_delete`, `project_match`, `project_create`, `project_list`,
`project_get`, `project_update`, `apikey_create`, `apikey_list`, and
`apikey_revoke`. Every one is also reachable from the CLI via
`satellites exec <verb> --json '<args>'`; the dispatch path is shared,
so behaviour is byte-identical to the MCP call.

Array-typed fields (`tags`, `versions`, etc.) must be passed as real
JSON arrays — `["epic:foo","area:bar"]`, never the stringified form
`"[\"epic:foo\"]"`. The dispatcher rejects the stringified shape with
a bad-request error.

Use `<target_install_path> --help` for the command tree. Typed
subcommands cover the high-traffic verbs:

| Group         | Use                                                                |
| ------------- | ------------------------------------------------------------------ |
| `project`     | `match --remote <url>` resolves a git remote to a `project_id`.    |
| `exec <verb>` | Direct verb dispatch — JSON in, JSON out. Covers every verb in the registry, including the four `document_*` verbs. |
| `version`     | Prints the CLI's stamped version.                                  |

### Stories are documents with `type:"story"`

There is no `satellites story <op>` subcommand and no `story_*` verb.
Story workflows go through the `document_*` verbs by passing
`type:"story"` (or addressing by `id` for updates/reads/deletes). The
`document_upsert` "upsert modes" table below has the exact request
shapes; the same shapes apply to MCP and CLI calls.

| Story action  | CLI call                                                                                                         |
| ------------- | ---------------------------------------------------------------------------------------------------------------- |
| Create story  | `satellites exec document_upsert --json '{"type":"story","project_id":"proj_…","name":"Title","body":"…","tags":["epic:foo"]}'` |
| Read story    | `satellites exec document_get --json '{"id":"sty_…"}'`                                                          |
| Update story  | `satellites exec document_upsert --json '{"id":"sty_…","status":"in_progress","tags":["…"]}'`                  |
| Delete story  | `satellites exec document_delete --json '{"id":"sty_…"}'`                                                      |
| List stories  | `satellites exec document_list --json '{"type":"story","project_id":"proj_…"}'`                                |

Story field reference + tag conventions: `docs/story-schema.md` in the
satellites repo.

When `--project-id` is omitted on an operational verb, the CLI falls
back to the TOML's `project_id`. If neither is set, the CLI returns
`project_id not defined`. Treat that error as bootstrap drift: you
are responsible for repair. Re-run Step 4 (call `project_match` on
the consumer repo's git remote, write the result into the TOML), then
retry the verb. The CLI does not self-repair the TOML.

## MCP-only clients (no CLI install)

Hosted assistants (Claude web, etc.) can't shell out to the CLI. For
them, the MCP server exposes the `document_*` verbs covering both
free-form documents and stories, the `project_*` verbs needed to
register and maintain projects without the CLI, and the `apikey_*`
verbs that mint, list, and revoke api-keys in-band. Stories are
documents with `type:"story"`; there is no separate `story_*`
surface. The Bearer credential on the MCP session authorises each
call — no TOML, no installed binary.

| Verb              | Use                                                                                          |
| ----------------- | -------------------------------------------------------------------------------------------- |
| `document_get`    | Fetch by `id` (any document, including stories) OR by `(scope, name)` for free-form documents. |
| `document_list`   | Paginated list with structured filters. Pass `type:"story"` to list stories, `type:"document"` for documents, omit `type` for both. |
| `document_upsert` | Create or update. See "upsert modes" below — three call shapes serve story-create, story-update, and document-upsert.   |
| `document_delete` | Delete by `id` (hard-delete for stories, soft-tombstone for documents) OR by `(scope, name)` (soft-tombstone).         |
| `project_match`   | Resolve a `project_id` from a git remote URL. Returns `not_found` when the project isn't registered yet. |
| `project_create`  | Register a new project under a workspace. Required when `project_match` returns `not_found` and the caller has no CLI. Body: `{"name":"…","git_url":"…","description":"…"}`. `workspace_id` falls back to the default workspace when omitted. |
| `project_list`    | List projects, optionally filtered by `workspace_id`.                                                                  |
| `project_get`     | Fetch a single project by `id`.                                                                                        |
| `project_update`  | Patch mutable fields (`name`, `description`, `git_url`) on a project.                                                  |
| `apikey_create`   | Mint a project-scoped api-key for the authenticated caller. Body: `{"workspace_id":"wksp_…","project_id":"proj_…","agent_name":"<slug>","scopes":[…]}`. Caller must be a member of the target workspace. Returns the raw `apikey` (only visible at mint time) plus `key_id`. |
| `apikey_list`     | List the authenticated caller's non-revoked api-keys, filtered by any combination of `workspace_id` / `project_id` / `agent_name`. Returns redacted rows (no raw key). |
| `apikey_revoke`   | Revoke one of the authenticated caller's api-keys by `key_id`. Existing keys belonging to other callers cannot be revoked. |

### Upsert modes (document_upsert)

`document_upsert` chooses its mode on inspection of the request:

| Shape | Mode |
| ----- | ---- |
| `{"type":"story", "project_id":"proj_…", "name":"My story", "body":"…", "tags":["epic:foo"]}` | Story create. Mints a fresh `sty_<id>`, inserts the row, fires reviewer + ledger + summary hooks. |
| `{"id":"sty_…", "status":"in_progress", "tags":["…"], "body":"…"}` | Story update. Patches metadata in place; body change appends a new `document_versions` row. Pointer-shaped fields (`tags`, `status`, `priority`, `category`, `parent_id`, `acceptance_criteria`) ignore omitted keys. |
| `{"type":"document", "scope":"project", "workspace_id":"…", "project_id":"…", "name":"release-notes", "body":"…"}` | Document upsert. Key-addressed; first call inserts, subsequent calls append a new version. |

### List filter shape (document_list)

Stories and documents are the same substrate kind (one row in the
`documents` table, distinguished by `type`). One `document_list` verb
covers both, with structured filters:

```json
{
  "type": "story",                  // or "document" or "" / "all" for both
  "scope": "project",               // optional: system | workspace | project
  "workspace_id": "wksp_…",         // optional
  "project_id":   "proj_…",         // optional
  "tags": ["epic:foo", "area:bar"], // AND filter
  "status": "in_progress",          // "all" / omit disables
  "name_prefix": "release-",        // case-insensitive prefix on `name`
  "limit": 50,                      // default 50, max 200
  "cursor": ""                      // opaque, from previous page's next_cursor
}
```

Response:

```json
{
  "items": [ { /* document.Document */ } ],
  "next_cursor": "…"   // empty when no more pages
}
```

Pass `next_cursor` back as `cursor` on the next call. Ordering is
`created_at DESC, id DESC` so cursors stay stable under inserts.

Field shapes for stories: see `docs/story-schema.md` in the satellites
repo. Tag conventions (`epic:<slug>`, `area:<topic>`, `priority:<level>`)
are the same ones the CLI uses, so MCP-only and CLI-driven authors share
one taxonomy.

`project_id` is required on `document_upsert` for `type:"story"` (the
story create mode) and on workspace/project-scoped document upsert /
delete. Use `project_match` to resolve it from the operator's repo URL
when the operator names a repo rather than an id.
