# File-based documents — `.satellites/documents/`

Operator-authored documents live in the consumer repo under
`.satellites/documents/` and reach the substrate via the
`satellites documents upload` CLI command. The tree is version-
controlled (the `.gitignore` excepts it from the broader `.satellites/`
ignore rule) so principle bodies and free-form documents stay in git
alongside the code they govern.

## Layout

Scope is inferred from the path. The CLI understands two shapes:

```
.satellites/documents/
├── workspace/
│   └── <workspace_id>/
│       └── <name>.md
└── project/
    └── <workspace_id>/
        └── <project_id>/
            └── <name>.md
```

| Path shape                                                  | Scope     |
| ----------------------------------------------------------- | --------- |
| `workspace/<workspace_id>/<name>.md`                        | workspace |
| `project/<workspace_id>/<project_id>/<name>.md`             | project   |
| anything else                                               | skipped   |

System-scope documents are NOT in this tree. They ship with the
binary via `config/seed/system/artifacts/` and reach the substrate
through the boot reconciler — operators cannot mutate them at runtime
by design (see `docs/seeds.md` for the system-seed registry).

## File format

Markdown with optional YAML frontmatter. Frontmatter recognised keys:

```
---
tags: [principles:project, area:auth]
name: explicit-name-override
---
# Body starts here
```

| Frontmatter key | Effect on the upsert                                                          |
| --------------- | ----------------------------------------------------------------------------- |
| `tags`          | Applied to `documents.tags` via the substrate's tag-merge path. Idempotent.   |
| `name`          | Overrides the filename-derived name. Without it, the filename stem is used.   |

A file without frontmatter is treated as an untagged document with its
filename stem (kebab-case) as the name. The unit of identity is
`(scope, workspace_id, project_id, name)` — moving a file under a
different scope subtree creates a different document.

## Tagging principles

Tag a file `principles:<scope>` to surface it through the ride-along
sidecar on the matching read verbs (see `docs/principle-loading.md`).
The scope in the tag must match the directory the file lives under —
the loader does not enforce alignment, but a mismatch produces a
principle that never rides along.

Examples:

| File                                                                            | Tag                |
| ------------------------------------------------------------------------------- | ------------------ |
| `workspace/<wksp>/team-rules.md`                                                | `principles:workspace` |
| `project/<wksp>/<proj>/commit-push-after-each-story.md`                         | `principles:project` |
| `project/<wksp>/<proj>/story-execution-rule.md` (one of several story-only)     | `principles:story` |

## Pushing

```
satellites documents upload --dry-run   # list planned dispatches
satellites documents upload             # apply each file
```

Idempotent. A re-push of unchanged files produces zero new
`document_versions` rows and zero `documents.updated_at` bumps. The
output reports one line per file with the resolved target and a short
status:

```
.satellites/documents/project/wksp_…/proj_…/commit-push-after-each-story.md → project/wksp_…/proj_…/commit-push-after-each-story (version=1)
```

Flags:

- `--dry-run` — print plan without dispatching.
- `--config <path>` — override the `satellites.toml` walk-up.
- `--user <id>` — override `$SATELLITES_USER_ID` (in-process dispatch).

Exit code: `0` on success (including the "no change" branch); non-zero
on transport failure, permission denial, or a malformed file.

## When to use this vs `document_upsert` directly

| Use this | When |
| --- | --- |
| `satellites documents upload` | Bulk apply / sync from the repo. Principles, runbooks, project conventions — anything that should live next to the code. |
| `document_upsert` verb directly | One-off edits from the portal, scripts, or other tools that don't have repo-resident files. |

Both write to the same substrate row when keyed identically.
