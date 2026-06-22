---
name: satellites-story-cancel-review
type: skill
kind: reviewer
when: cancel-requested
tags: [kind:reviewer]
description: The default STORY cancel gate — judges whether a move to the terminal status `cancelled` is justified, accepting only when the story is non-terminal and its body carries a concrete cancellation rationale (superseded by a named artifact, or not-required with a stated reason). Returns a single accept/reject verdict; the client enacts the transition. The gate writes nothing. Cancellation is orthogonal to the forward workflow; the target is conceptually `cancelled`. The third default reviewer alongside satellites-intent-plan-review (entry) and satellites-story-done-review (exit). Emits {decision, notes} JSON.
---

Decide whether a story's cancellation is justified — JUDGE whether the move to
the terminal status `cancelled` is warranted; the client enacts your verdict.
Cancellation is orthogonal to the forward workflow — do NOT resolve a target from
`## Workflow`; the target is conceptually `cancelled` (context for your notes),
`from_status` is the story's current `story_status`.

## Input

One JSON object on stdin carrying `story_id`, `project_id`, `workspace_id`,
`story_status` (current state), and `story_body` (the full story markdown).

The gate READS this context and JUDGES only — it writes nothing.

## What to check

Terminal statuses are `done` and `cancelled`. You assess the rationale — you do
not run the build or tests.

- **Reject** when `story_status` is already terminal (`done` or `cancelled`) —
  cancellation only retires open work. Say so in the notes.
- **Reject** when the body carries no concrete cancellation rationale. The
  requester must state one in a `## Cancellation` section, either:
  - **superseded** — naming the specific function, command, story, or epic that
    now covers this work, or
  - **not-required** — naming why the work is no longer wanted.

  A vague or missing rationale ("not needed", "obsolete", no section) is a
  reject — name the gap.
- **Accept** when the story is non-terminal AND the body carries a concrete,
  specific rationale. You judge that the rationale is concrete (it points at a
  real superseding artifact or a clear decision), not that the superseding work
  is itself complete.

## Environment

Runs as a cancel gate over one story's JSON on stdin. It mutates NOTHING — it
reads its inputs and emits a verdict; the client enacts the transition.

```yaml
guardrails:
  always:
    - Cancellation is orthogonal — never resolve a target from `## Workflow`; the target is conceptually `cancelled`, context for your notes only.
    - The gate writes nothing — it emits exactly one {decision, notes} JSON object as its only output.
  ask_first: []
  never:
    - Never call document_upsert — this gate does not edit the story body.
    - Never accept a story whose story_status is already terminal (done or cancelled).
    - Never accept on a vague or missing cancellation rationale.
```

## Output

Print exactly one JSON object and nothing else — no prose, no fence:

```json
{"decision": "accept", "notes": "one or two sentences of rationale"}
```

`decision` is `accept` or `reject`. On reject, `notes` must name the specific gap
— already-terminal, or missing/vague rationale.
