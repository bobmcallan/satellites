# Story schema

A **story** is the substrate's unit of work — every requirement, bug,
or improvement an operator wants tracked is a row in the `stories`
table bound to a `project_id`. The schema is intentionally minimal:
the verb layer enforces only the invariants needed to keep rows
queryable, and the rest is convention. Reviewers and downstream
agents (`epic:story-reviewer-process`) check the conventions on top
of the schema, not in place of it.

## Fields

| Field                 | Required | Type     | Notes                                                                                                                              |
| --------------------- | -------- | -------- | ---------------------------------------------------------------------------------------------------------------------------------- |
| `id`                  | auto     | string   | `sty_<8hex>`. Stamped by the store on create; immutable.                                                                           |
| `project_id`          | yes      | string   | The owning project. Cross-project moves are not supported via `story_update`.                                                       |
| `parent_id`           | no       | string   | Another `sty_…` for epic → child decomposition. Pass `--parent-id ""` to clear.                                                    |
| `title`               | yes      | string   | One-line objective. Cannot be cleared on update. The reviewer agent flags vague or imperative-poor titles.                          |
| `body`                | no       | string   | Problem statement + approach. Markdown. Embed links to related stories/PRs/docs here using the `story:<id>` / `epic:<slug>` prefix forms. |
| `acceptance_criteria` | no       | string   | Numbered, testable outcomes. The reviewer agent flags missing or vague AC.                                                          |
| `status`              | no       | enum     | `backlog` (default) · `ready` · `in_progress` · `review` · `done` · `cancelled`.                                                   |
| `priority`            | no       | enum     | `critical` · `high` · `medium` (default) · `low` · `none`.                                                                          |
| `category`            | no       | enum     | `feature` (default) · `bug` · `improvement` · `infrastructure`. Operator-defined values are tolerated but unconventional.           |
| `tags`                | no       | string[] | Free-form. Conventions: `epic:<slug>`, `epic-order:<n>`, `area:<area>`, `blocked-by:<sty_id>`. Tags are AND-filtered by `story list --tag`. |
| `created_at`          | auto     | UTC time | Stamped by the store.                                                                                                              |
| `updated_at`          | auto     | UTC time | Refreshed on every successful update; equal to `created_at` on first insert.                                                       |

## Conventions

The reviewer agent rubric and operator habits agree on a few patterns
that the schema does not enforce:

- **Purpose** lives in the first paragraph of `body` — one paragraph
  answering *why this exists*, before the approach narrative. Stories
  with no purpose paragraph read as task-stubs and are flagged.
- **Context links** (related stories, PRs, design docs) live inline in
  `body`, written as `story:<id>` / `epic:<slug>` / `document:<scope>/<name>`
  prefixes. The MCP load-context tells operator agents to resolve these
  through the CLI; the prefix form is the canonical reference shape.
- **Epic membership** is expressed two ways at once: `parent_id`
  pointing at the epic's anchor story for traversal, and an
  `epic:<slug>` tag for filtering. Children typically also carry an
  `epic-order:<n>` tag for sequencing.
- **Blockers** are expressed as `blocked-by:<sty_id>` tags. They are
  documentary only — the verb layer does not refuse mutations on
  blocked stories. Operators (or the reviewer agent) are expected to
  drop stale blockers when the dependency lands.

## Minimum invariants

The verb layer enforces:

- `project_id` is non-empty on `story_create`.
- `title` is non-empty on `story_create`, and cannot be cleared on
  `story_update` (passing `--title ""` returns an error).
- `parent_id`, when set, is stored verbatim — referential integrity to
  another story row is *not* checked at write time. A broken
  `parent_id` surfaces as a missing-parent finding when the reviewer
  walks the project.

Everything else (well-formed AC, context links, epic membership tags,
clear purpose paragraph) is **convention** and lives in the reviewer
agent's markdown rubric (`config/documents/story_reviewer.md`, a type:"skill" row), not in
Go source.

## Deletion semantics

`story_delete` is a hard delete (no soft-delete tombstone — the
`cancelled` status already covers "kept for visibility"). Two
substrate invariants govern what survives:

- **Children stay** — the self-FK on `parent_id` is `ON DELETE SET
  NULL` (migration 0007). Child rows whose `parent_id` pointed at the
  deleted story have their `parent_id` cleared atomically; the
  children themselves are untouched.
- **Ledger entries stay** — the `evidence` table is append-only at the
  trigger layer (migration 0001) and has no FK to `stories`. Entries
  authored against the deleted story persist as audit history, now
  orphaned. This is the only behaviour compatible with the
  append-only invariant.

## Well-formed example

```text
title:              CLI: self-heal missing project_id from git remote on operational verbs
status:             ready
priority:           high
category:           feature
parent_id:          sty_10c3536f
tags:
  - epic:bootstrap-autonomy
  - epic-order:2
  - area:bootstrap
  - area:cli

body: |
  Today the CLI requires project_id in .satellites/satellites.toml
  (or --project-id) for every operational verb. A returning-session
  TOML written by an older flow may lack the field; nothing in the
  CLI recovers — it returns "project_id not defined". The
  load-context's Step 4 places the burden on the agent to remember
  to call project_match, which is fragile across sessions and across
  consumer-side agent variants.

  Move the recovery into the CLI itself: when an operational verb
  runs without project_id, attempt `git remote get-url origin` from
  the repo root, call project_match, persist the result to the TOML,
  and stderr-log what happened before continuing the verb.

  Related: story:sty_10c3536f (epic anchor), document:system/satellites_mcp_load_context (Step 4).

acceptance_criteria: |
  1) Any operational verb launched without project_id in TOML and
     without --project-id falls into a self-heal path before failing.
  2) Self-heal: read git remote of repo_path; call project_match; on
     success write project_id into the TOML in place, log to stderr
     the resolved id and matched_url, continue the original verb.
  3) On project_match not_found or non-git repo_path: surface the
     original "project_id not defined" error unchanged.
  4) Self-heal is opt-out via SATELLITES_NO_AUTO_PROJECT_ID=1.
  5) DOGFOOD: edit .satellites/satellites.toml to remove project_id,
     run `satellites story list`, expect (a) verb succeeds, (b) TOML
     now contains project_id matching project_match's result, (c)
     stderr line names the matched_url. Run again, expect no further
     self-heal log.
```

This story is well-formed because:

- **Title** names the change in one imperative line.
- **Purpose paragraph** (first paragraph of `body`) states *why this
  exists* — the gap left by the load-context-only repair path.
- **Approach paragraph** follows, describing *how* the fix is shaped.
- **Context links** appear inline as `story:<id>` and
  `document:<scope>/<name>` prefixes.
- **Acceptance criteria** are numbered and testable, including a
  dogfood step against the real environment.
- **Epic membership** is expressed by `parent_id` + matching
  `epic:<slug>` and `epic-order:<n>` tags.
- **Category and priority** are the substrate defaults' explicit
  variants — the reviewer agent does not flag default values.
