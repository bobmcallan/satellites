# Principle loading

Principles are small, prescriptive substrate documents that ride along on
the verbs the agent already calls. There is no separate "load principles"
step — the substrate attaches matching principles to the response of every
verb that crosses a scope boundary.

## What makes a document a principle

Any `type:"document"` tagged `principles:<scope>`, where `<scope>` is one
of:

| Scope     | Tag                    | Delivered on the response of                                        |
| --------- | ---------------------- | ------------------------------------------------------------------- |
| global    | `principles:global`    | MCP `initialize` (the load-context push every session receives)     |
| workspace | `principles:workspace` | `project_match`, `project_get`, `project_list` — scoped to the workspace in the response |
| project   | `principles:project`   | same as workspace, scoped to the project; also `document_get` on a story (via the story's project) |
| story     | `principles:story`     | `document_get` on a story; `story_request_review`                  |

There is no `type:"principle"`. A principle is a document plus a tag, so
all CRUD goes through the existing verbs (`document_upsert`,
`document_get`, `document_list`, `document_delete`).

## Content rules

- **Short.** One screen. If it would be a manual, it is not a principle.
- **Prescriptive.** Imperative voice; rationale lines no longer than one
  per rule.
- **Repo-agnostic.** No identifiers, file paths, or tool names that will
  rot. Reference tool names only when the rule is *about* that tool.

The `satellites-audit-agent-prose` skill enforces these rules. Run it
before committing any principle body.

## Loading lifecycle — worked example

A fresh Claude session is told: *"implement `story_abc123`"*.

| # | Agent action                                  | Substrate response                       | Principles delivered                                                  |
| - | --------------------------------------------- | ---------------------------------------- | --------------------------------------------------------------------- |
| 1 | MCP `initialize` (automatic on connect)       | load-context body + sidecar              | `principles:global`                                                   |
| 2 | `project_match {git_url: <consumer remote>}`  | `{project_id, workspace_id, ...}`        | `principles:workspace` + `principles:project` for the matched IDs    |
| 3 | `document_get {id: "story_abc123"}`           | story body + sidecar                     | `principles:story` for the story's project                            |
| 4 | edit story body (`document_upsert` on body)   | row returned without principle sidecar   | none — write paths do not re-deliver                                  |
| 5 | `story_request_review {story_id}`             | `{decision, notes, new_status}` + sidecar | `principles:story` re-delivered with the verdict                     |

Every call that *crosses* a scope re-delivers the principles for that
scope, fresh. Memory is not used to carry principles between calls. This
is the design: a principle that is stale, overridden, or ignored is stale,
overridden, or ignored on every substrate call — not invisibly.

## What does not happen

- No `principles_get` / `principles_load` verb. Principles ride along;
  the agent never fetches them on their own.
- No eager pre-load at session start beyond the global set. Workspace and
  project principles wait for the workspace and project to be named.
- No automatic enforcement. Principles are prose the agent reads. If a
  rule must be a hard gate, encode it in a reviewer skill instead.

## Authoring

Project-scope example:

```
document_upsert {
  "type":"document",
  "scope":"project",
  "workspace_id":"wksp_…",
  "project_id":"proj_…",
  "name":"<kebab-case-name>",
  "body":"…",
  "tags":["principles:project"]
}
```

The next ride-along call that crosses the principle's scope picks up the
new body — no cache, no session restart.

## Operator visibility

Principles are normal documents:

- Listable: `document_list type:"document" tags:["principles:project"]`.
- Editable: portal renders any document body; v5-portal-project-config
  surfaces the project-scoped principles list next to the project config.
- Versioned: every `document_upsert` appends a `document_versions` row.

## Related

- `docs/document-types-v5.md` — substrate primitives behind these
  documents.
- `document:project/story-execution-process` — example principle (project
  scope) covering per-story execution rules.
