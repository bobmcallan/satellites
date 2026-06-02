# Document types — v5

The v5 substrate is three document shapes on top of the existing `documents` + `evidence` tables. **No new tables, no new types** beyond what `TypeStory` / `TypeDocument` / `TypeTask` and the ledger primitive already provide. This document is the canonical convention reference for agents, reviewers, and operators.

## 1. Story body conventions

`type:"story"`. Already supported. v5 layers conventions on the body, not on the schema.

A story body should contain, in this order:

1. **Title heading** (`# v5: …`) — matches the document `name`.
2. **Epic line** — `**Epic:** epic:<slug>` so the epic membership is visible inline as well as via the `epic:<slug>` tag.
3. **Purpose** — one paragraph, what the story exists to do.
4. **In scope** — bulleted list, narrow and concrete.
5. **Out of scope** — bulleted list, the temptations to avoid.
6. **Acceptance criteria** — bulleted, each one independently checkable.
7. **Estimate** — `S / M / L` + days.
8. **Blocked** (only when present) — added by the agent when the [story execution process](../README.md) says to stop. Holds the reason + what the operator must supply.

The `status` field on the documents row tracks workflow position (`backlog`, `in_progress`, `completed`, `cancelled`, etc.). Allowed values are declared per story-type by the workflow skill (see [v5-workflow-skill-spec](https://github.com/bobmcallan/satellites) story); the substrate itself accepts any string.

Tag conventions (already in use across the repo):

- `epic:<slug>` — epic membership
- `area:<topic>` — coarse area filter
- `epic-order:<N>` — display order within the epic
- `rebase:<vN>` — flagged as part of a rebase set
- `superseded-by:<slug>` — tombstone pointing to the replacement
- `dogfood-after:<story-name>` — operator-refresh gate; see `document:project/story-execution-process`

## 2. `project-config` document convention

A single `type:"document"`, `scope:"project"`, `name:"project-config"` document per project. Free-form body. Workflow skills, reviewers, and the loop verb read it; humans edit it (eventually via the portal — see v5-portal-project-config story).

**Why `type:"document"` and not a new `type:"project_config"`?** Because adding a type buys nothing: scope + name is already unique per project, the listing surface already filters by `name_prefix`, and the workflow skill knows what to look for. Inventing a type would multiply code paths without semantic benefit.

**Body shape (slim, post-sty_815c09e7):**

Story-type → workflow dispatch is **no longer** in this document. It is
derived from the **dynamic skill index**: the `kind:workflow` skill whose
`applies_to` frontmatter contains the story type (`satellites skill index`).
The `story_types` map and `reviewer_overrides` were retired with the
server-side `story_request_review` verb (sty_6e1c3641). What remains is the
residual post-transition settings the index cannot express:

```yaml
# project-config
#
# Dispatch (story type → workflow) comes from the dynamic skill index
# (skill `applies_to`), NOT this document. Only post-transition settings
# live here.

step_summariser_skill: satellites-story-summary   # optional; per-transition step summary
```

**Fetching:**

```
document_get scope=project name=project-config workspace_id=<wksp> project_id=<proj>
```

**Editing:** today, via `document_upsert` (creates a new version per edit). The v5-portal-project-config story adds a UI for this.

## 3. Ledger reference

The "ledger" the v5 epic and README talk about is the existing **`evidence`** table, exposed through the `ledger_append` and `ledger_list` verbs (see `internal/ledger/` and `internal/verb/ledger.go`). It is the substrate's append-only event log — **append-only enforced at the DB trigger layer**, not the verb layer.

Ledger entry id format: `evt_<8hex>`.

Entry shape (per `ledger.Entry`):

```json
{
  "id":         "evt_a1b2c3d4",
  "story_id":   "sty_4414dfbe",
  "kind":       "story_updated",
  "actor":      "usr_…",
  "body":       "free-form text the entry is about",
  "payload":    { "...": "structured JSON" },
  "refs":       [ "..." ],
  "created_at": "2026-05-27T09:34:52Z"
}
```

Canonical `kind` discriminators the substrate emits (see `internal/ledger/ledger.go`):

- `story_created`
- `story_updated`
- `review_finding`
- `summary_updated`
- `comment`

Reviewers and the loop verb (v5-story-request-review) will emit additional kinds (`review_accept`, `review_reject`, `status_transition`, etc.) — the verb layer takes any string; the canonical set above just covers what the substrate itself writes today.

**Usage:**

| Operation | Verb | Notes |
|---|---|---|
| Append an entry | `ledger_append` | Requires `story_id` + `kind`. Actor inferred from auth context if not set. |
| Read entries for a story | `ledger_list` | Returns oldest-first. Optional `kind` filter. |
| Update / delete | (none) | DB rejects UPDATE and DELETE on `evidence` rows. |

The ledger is the immutable log; the story body is the mutable context. Agents read both, reviewers append to the ledger when they accept/reject, and human readers look at either via the portal.

## Adding new document conventions

Future v5 documents (workflow skill mapping, reviewer rubrics, etc.) follow the same shape: pick the smallest existing type (`document` unless there's a strong reason), use a clear `name`, document the body convention here. New types or tables require a separate story and a justification beyond "feels cleaner."
