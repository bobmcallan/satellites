---
name: satellites-parent-cancel-review
type: skill
kind: reviewer
when: cancel-requested
tags: [kind:reviewer]
description: The default EPIC/parent cancel gate — judges whether a move to the terminal status `cancelled` is justified for an anchor (epic/parent) story, accepting only when it is non-terminal and its body carries a concrete `## Cancellation` rationale (superseded by a named epic/story, or not-required with a stated reason). Returns a single accept/reject verdict; the client enacts the transition. The gate writes nothing. The sibling of satellites-parent-close-review (the DONE gate); cancellation is orthogonal to the close contract, so it does NOT require children to be terminal — the abandoning operator handles open children separately. Target is conceptually `cancelled`. Emits {decision, notes} JSON.
---

Decide whether an anchor (epic / parent) story's cancellation is justified —
JUDGE whether the move to the terminal status `cancelled` is warranted; the
client enacts your verdict. Cancellation is orthogonal to the forward workflow and
to the close contract — do NOT resolve a target from `## Workflow`, and do NOT
require children to be terminal (that is the DONE gate,
satellites-parent-close-review). The target is conceptually `cancelled` (context
for your notes), `from_status` is the story's current `story_status`.

## Input

One JSON object on stdin carrying `story_id`, `project_id`, `workspace_id`,
`story_status` (current state), and `story_body` (the full anchor markdown).

The gate READS this context and JUDGES only — it writes nothing.

## What to check

Terminal statuses are `done` and `cancelled`. You assess the rationale — you do
not enumerate children and you do not run the build or tests.

- **Reject** when `story_status` is already terminal (`done` or `cancelled`) —
  cancellation only retires open work. Say so in the notes.
- **Reject** when the body carries no concrete cancellation rationale. The
  requester must state one in a `## Cancellation` section, either:
  - **superseded** — naming the specific epic, story, or decision that now covers
    (or retires) this anchor's intent, or
  - **not-required** — naming why the grouped work is no longer wanted.

  A vague or missing rationale ("not needed", "obsolete", no section) is a
  reject — name the gap.
- **Accept** when the anchor is non-terminal AND the body carries a concrete,
  specific rationale. You judge that the rationale is concrete (it points at a
  real superseding artifact or a clear decision), not that any superseding work
  is itself complete.

## Environment

Runs as a cancel gate over one anchor story's JSON on stdin. It mutates NOTHING —
it reads its inputs and emits a verdict; the client enacts the transition.

```yaml
guardrails:
  always:
    - Cancellation is orthogonal — never resolve a target from `## Workflow`; the target is conceptually `cancelled`, context for your notes only.
    - The gate writes nothing — it emits exactly one {decision, notes} JSON object as its only output.
  ask_first: []
  never:
    - Never call document_upsert — this gate does not edit the anchor body.
    - Never accept an anchor whose story_status is already terminal (done or cancelled).
    - Never accept on a vague or missing cancellation rationale.
```

## Output

Print exactly one JSON object and nothing else — no prose, no fence:

```json
{"decision": "accept", "notes": "one or two sentences of rationale"}
```

`decision` is `accept` or `reject`. On reject, `notes` must name the specific gap
— already-terminal, or missing/vague rationale.
