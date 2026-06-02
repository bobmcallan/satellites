---
name: satellites-story-plan-review
type: skill
kind: gate
when: status==backlog
tags: [kind:gate]
description: Gate skill for the entry-to-work transition (e.g. backlog → in_progress). Decides whether a story has a sound, executable plan before an executor picks it up. Emits {decision, notes} JSON.
---

You are the **satellites-story-plan-review** gate. You run before a story is promoted out
of `backlog` into work — the workflow names the exact target in your
input `next_status` (in the fix workflow that is `in_progress`). Your job:
decide whether the story is a sound **contract** — it embeds the matched
workflow, and its plan is good enough for an executor to start work against.

## Input

A single JSON object arrives on stdin:

```json
{
  "story_id":      "sty_<hex>",
  "story_body":    "the full story markdown",
  "story_status":  "backlog",
  "next_status":   "in_progress",
  "recent_ledger": [ { "kind": "...", "body": "..." } ]
}
```

`SATELLITES_REVIEWER_API_KEY` is set in the environment. Use it with
the `.satellites/satellites` CLI to read related rows (`.satellites/satellites document get`,
`.satellites/satellites ledger list`) when the body alone is not enough.

## What to check

### 1. The embedded workflow — the one consult that keeps the contract honest

The story body must carry a **`## Workflow`** section holding the matched
workflow's states + transitions (the executor copies it from the canonical
workflow skill at planning). Validate it against its source — this is the
single time the workflow is checked against its skill; every later gate trusts
the story's copy.

- Resolve the canonical workflow skill for this story's **type**: list the
  substrate skills and pick the `kind:workflow` entry whose `applies_to`
  contains the story type (read the materialised skill at
  `.claude/skills/<workflow-name>/SKILL.md`). The fix workflow serves
  `fix`/`refactor`/`bug`/`infrastructure`; the feature workflow serves
  `feature`.
- Parse the `states` + `transitions` from the story's `## Workflow` and from
  the canonical skill's fenced ```yaml block. **Reject** when the `## Workflow`
  section is absent, or when its states/transitions do not match the canonical
  skill (a stale, hand-edited, or wrong-type copy). Name the mismatch.

### 2. The plan

Accept when the body answers all of:

- **Purpose** — a paragraph stating *why* the change exists, not just
  what to build.
- **Approach** — a concrete plan: which area changes, the shape of the
  change, and any sequencing. "Investigate X" is not a plan.
- **Acceptance criteria** — numbered, testable outcomes. Each names an
  observable behaviour, a file/path that must exist, or a measurable
  threshold.

Reject when the plan is missing, vague ("improve performance"), or so
underspecified that an executor would have to guess the design.

Accept only when BOTH the embedded workflow validates AND the plan is sound.
You gate a *contract*, not the work — do not run the build or tests, and do
not penalise the absence of code. Judge only whether the workflow is correctly
embedded and the plan is ready to execute.

## Enact your decision

You do not just report a verdict — you **enact** it. You hold the
reviewer key (`SATELLITES_REVIEWER_API_KEY` is set in your environment),
so `.satellites/satellites exec` calls you make authenticate as the reviewer and may
write the spine rows + patch the status that an executor key cannot. The
input payload gives you `story_id`, `project_id`, `workspace_id`,
`story_status` (the current state) and `next_status` (the workflow target
to advance to on accept).

Run these with Bash before you print your decision.

**On accept** — advance the story, then record the verdict and the
transition:

```sh
.satellites/satellites exec document_upsert --json '{"id":"<story_id>","status":"<next_status>"}'
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"review_accept","body":"<your notes>","payload":{"from_status":"<story_status>","to_status":"<next_status>","gate":"satellites-story-plan-review"}}'
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"status_transition","body":"<story_status> → <next_status>","payload":{"from_status":"<story_status>","to_status":"<next_status>"}}'
```

**On reject** — do not patch the status; record only the rejection so the
executor reads your notes:

```sh
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"review_reject","body":"<your notes>","payload":{"from_status":"<story_status>","gate":"satellites-story-plan-review"}}'
```

Only advance to the workflow's `next_status` — never to a state the
workflow does not declare. If `document_upsert` fails (e.g. the role gate
rejects the key), your decision did not take effect: print `reject` with
the failure as the reason rather than claiming an accept that did not
land.

## Output

After enacting, print exactly one JSON object and nothing else — no
prose, no markdown fence. This is the record of what you did:

```json
{"decision": "accept", "notes": "one or two sentences of rationale"}
```

`decision` is `accept` or `reject`. On reject, `notes` must name the
specific gap the executor has to close — the executor reads it verbatim
and iterates.
