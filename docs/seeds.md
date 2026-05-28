# Filebased seeds

Satellites carries operator-authored substrate prose at three scopes —
system, workspace, project — and each scope has its own delivery path:

| Scope     | Source of truth                                    | Lands in DB via                         |
| --------- | -------------------------------------------------- | --------------------------------------- |
| system    | `config/documents/*.md` (embedded)                 | `satellites-server` boot reconciler     |
| workspace | `.satellites/seeds/<workspace_id>/workspace.md`    | `satellites seed push`                  |
| project   | `.satellites/seeds/<workspace_id>/<project_id>/project.md` | `satellites seed push`          |

This document covers the **workspace** and **project** paths.
See `docs/ARCHITECTURE.md` (System seed registry) for the system path —
mutating those requires a binary release by design.

## Layout

Seeds live in the consumer repo at `.satellites/seeds/`, the only
subtree of `.satellites/` that is version-controlled
(`.gitignore` ignores `.satellites/` wholesale but excepts
`!.satellites/seeds/` — same pattern as tracking `.claude/skills/`
inside an otherwise-ignored `.claude/`).

```
.satellites/
└── seeds/
    └── <workspace_id>/
        ├── workspace.md                # applied to workspaces.seed_md
        └── <project_id>/
            └── project.md              # applied to projects.seed_md
```

Path depth is the dispatch contract:

- two segments + filename `workspace.md` → `workspace_seed_apply`
- three segments + filename `project.md` → `project_seed_apply`
- anything else → skipped silently

The repo is the source of truth. Portal edits to `seed_md` do **not**
survive a subsequent `satellites seed push` — re-pushing overwrites in
full. If portal-side editing is required for a particular row, don't
ship a seed file for it.

## Apply: `satellites seed push`

`satellites seed push` walks `.satellites/seeds/` from the current
working directory and dispatches each matching file via the right verb.
Idempotent — the substrate short-circuits when the incoming body
matches the stored `seed_md` byte-for-byte, so re-pushing an unchanged
file is a zero-write no-op. The operator sees `applied` or `no change`
per file.

Flags:

- `--dry-run` — print the planned dispatches without calling the verbs.
  Use this to confirm what a push would touch.
- `--config <path>` — override the `satellites.toml` walk-up.
- `--user <id>` — override `$SATELLITES_USER_ID` (in-process dispatch).

Exit code: `0` on success (including the "no change" branch); non-zero
on transport failure, permission denial, or a malformed seed file.

### Example

```
$ tree .satellites/seeds
.satellites/seeds
└── wksp_6f048cd8
    ├── proj_fc7d72d8
    │   └── project.md
    └── workspace.md

$ satellites seed push --dry-run
[dry-run] .satellites/seeds/wksp_6f048cd8/workspace.md → workspace_seed_apply
[dry-run] .satellites/seeds/wksp_6f048cd8/proj_fc7d72d8/project.md → project_seed_apply

$ satellites seed push
.satellites/seeds/wksp_6f048cd8/workspace.md → applied
.satellites/seeds/wksp_6f048cd8/proj_fc7d72d8/project.md → applied

$ satellites seed push           # re-run, nothing changed
.satellites/seeds/wksp_6f048cd8/workspace.md → no change
.satellites/seeds/wksp_6f048cd8/proj_fc7d72d8/project.md → no change
```

## Verbs

Both verbs accept `{id, body}` and full-replace the seed body. The
response is `{<row>, applied, reason?}` — `applied=true` when bytes
differed, `applied=false` with `reason="no change"` when re-applying
the same content.

| Verb                    | Request                                  |
| ----------------------- | ---------------------------------------- |
| `workspace_seed_apply`  | `{"workspace_id": "<wksp_…>", "body": "…"}` |
| `project_seed_apply`    | `{"project_id":   "<proj_…>", "body": "…"}` |

Authorization (HTTP/MCP path): caller must be a member of the target
workspace (project verbs resolve the workspace from the project row
first, then enforce membership). CLI-local in-process invocations
bypass — same posture as `document_upsert`.
