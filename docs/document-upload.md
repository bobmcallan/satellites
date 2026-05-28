# File-based documents — `.satellites/documents/`

Operator-authored documents live in the consumer repo under
`.satellites/documents/` and reach the substrate via the
`satellites document upload` CLI command. The tree is version-
controlled (the `.gitignore` excepts it from the broader `.satellites/`
ignore rule) so principle bodies and free-form documents stay in git
alongside the code they govern.

## Layout

Flat. Every document is a sibling `.md` file directly under
`.satellites/documents/`:

```
.satellites/documents/
├── commit-push-after-each-story.md
├── team-rules.md
└── …
```

The classifier reads scope and identity from each file's YAML
frontmatter, not from the directory tree. Subdirectories under
`.satellites/documents/` are skipped — the convention IS the contract.

System-scope documents are NOT in this tree. They ship with the
binary via `config/documents/` and reach the substrate through the
boot reconciler — operators cannot mutate them at runtime by design
(see `docs/seeds.md` for the system-seed registry).

## File format

Markdown with required YAML frontmatter:

```
---
name: commit-push-after-each-story
scope: project
workspace_id: wksp_6f048cd8
project_id: proj_fc7d72d8
tags: [principles:project]
---
# Body starts here
```

| Frontmatter key | Required when                                  | Effect                                                                          |
| --------------- | ---------------------------------------------- | ------------------------------------------------------------------------------- |
| `scope`         | always                                         | One of `workspace` or `project`. Missing scope is a hard error.                 |
| `workspace_id`  | `scope: workspace` or `scope: project`         | The `wksp_…` identity for the target workspace.                                 |
| `project_id`    | `scope: project`                               | The `proj_…` identity for the target project.                                   |
| `name`          | optional                                       | Overrides the filename-derived name. Without it, the filename stem is used.     |
| `tags`          | optional                                       | Applied to `documents.tags` via the substrate's tag-merge path. Idempotent.     |
| `type`          | optional                                       | `document` (default) or `skill`. Routes the file to the matching substrate type.|

A file without `scope:` is rejected — the classifier no longer infers
scope from path segments. The unit of identity is
`(scope, workspace_id, project_id, name)` — changing any of those
frontmatter fields creates a different document.

## Tagging principles

Tag a file `principles:<scope>` to surface it through the ride-along
sidecar on the matching read verbs (see `docs/principle-loading.md`).
The scope in the tag must match the `scope:` frontmatter — the loader
does not enforce alignment, but a mismatch produces a principle that
never rides along.

Examples:

| `scope:` frontmatter | Tag                    |
| -------------------- | ---------------------- |
| `workspace`          | `principles:workspace` |
| `project`            | `principles:project`   |

## Pushing

```
satellites document upload --dry-run   # list planned dispatches
satellites document upload             # apply each file
```

Idempotent. A re-push of unchanged files produces zero new
`document_versions` rows and zero `documents.updated_at` bumps. The
output reports one line per file with the resolved target and a short
status:

```
.satellites/documents/commit-push-after-each-story.md → project/wksp_…/proj_…/commit-push-after-each-story (version=1)
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
| `satellites document upload` | Bulk apply / sync from the repo. Principles, runbooks, project conventions — anything that should live next to the code. |
| `document_upsert` verb directly | One-off edits from the portal, scripts, or other tools that don't have repo-resident files. |

Both write to the same substrate row when keyed identically.
