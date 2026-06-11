---
name: satellites-story-cancel-review
type: skill
kind: gate
when: cancel-requested
tags: [kind:gate]
description: Gate skill for cancelling a story — accepts a move to terminal `cancelled` only when the story is non-terminal and its body carries a concrete cancellation rationale. Emits {decision, notes} JSON.
---
<!-- satellites-sync:begin {"document_id":"doc_c41ce0cf","version":3,"hash":"a30836fc8a5027943c115663dccd490ddfd06554f38d3da9c4f5464a0972fda3"} satellites-sync:end -->

Decide whether a story's cancellation is justified, and if so enact it by moving the story to the terminal status `cancelled`. Cancellation is orthogonal to the forward workflow — do NOT resolve a target from `## Workflow`; the target is fixed: `to_status` is always `cancelled`, `from_status` is the story's current `story_status`.

## Input

One JSON object on stdin carrying `story_id`, `project_id`, `workspace_id`, `story_status` (current state), and `story_body` (the full story markdown).

The gate's `.satellites/satellites exec` calls authenticate as the operator's admin user, authorized to write status_transition / review_* rows.

## What to check

Terminal statuses are `done` and `cancelled`. You assess the rationale — you do not run the build or tests.

- **Reject** when `story_status` is already terminal (`done` or `cancelled`) — cancellation only retires open work. Say so in the notes.
- **Reject** when the body carries no concrete cancellation rationale. The requester must state one in a `## Cancellation` section, either:
  - **superseded** — naming the specific function, command, story, or epic that now covers this work, or
  - **not-required** — naming why the work is no longer wanted.

  A vague or missing rationale ("not needed", "obsolete", no section) is a reject — name the gap.
- **Accept** when the story is non-terminal AND the body carries a concrete, specific rationale. You judge that the rationale is concrete (it points at a real superseding artifact or a clear decision), not that the superseding work is itself complete.

## Enact

You enact your decision, you do not just report it. Your target is fixed: `to_status = "cancelled"`, `from_status = story_status`. Do NOT read `## Workflow` — there is no transition for cancellation. Run these with Bash before printing your decision.

**On accept** — two `ledger_append` calls:

```sh
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"review_accept","body":"<your notes>","payload":{"from_status":"<story_status>","to_status":"cancelled","gate":"satellites-story-cancel-review"}}'
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"status_transition","body":"<story_status> → cancelled","payload":{"from_status":"<story_status>","to_status":"cancelled"}}'
```

The `status_transition` row IS the status change.

**On reject** — record only the rejection, no status_transition:

```sh
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"review_reject","body":"<your notes>","payload":{"from_status":"<story_status>","gate":"satellites-story-cancel-review"}}'
```

If the status_transition `ledger_append` fails, the cancellation did not land — print `reject` with the failure as the reason.

## Environment

Runs as a `cancel-requested` gate over one story's JSON on stdin, writing to the ledger via `.satellites/satellites exec` under the operator's admin auth. It mutates external state (review rows and one status transition), so its tool use is bounded by the guardrails below.

```yaml
guardrails:
  always:
    - Target is fixed — to_status=cancelled, from_status=story_status; never resolve a target from `## Workflow`.
    - On accept, write both ledger_append rows (review_accept then status_transition); the status_transition row is what enacts the change.
    - Verify the status_transition ledger_append succeeded before printing accept; on its failure print reject with the failure as the reason.
    - Emit exactly one {decision, notes} JSON object as the only output.
  ask_first: []
  never:
    - Never call document_upsert — this gate does not edit the story body.
    - Never write a status_transition row on reject.
    - Never accept a story whose story_status is already terminal (done or cancelled).
    - Never accept on a vague or missing cancellation rationale.
```

## Output

After enacting, print exactly one JSON object and nothing else — no prose, no fence:

```json
{"decision": "accept", "notes": "one or two sentences of rationale"}
```

`decision` is `accept` or `reject`. On reject, `notes` must name the specific gap — already-terminal, or missing/vague rationale.
