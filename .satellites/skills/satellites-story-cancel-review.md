---
name: satellites-story-cancel-review
type: skill
kind: gate
when: cancel-requested
tags: [kind:gate]
description: Gate skill for cancelling a story — any non-terminal status → cancelled. Accepts only when the story body carries a concrete cancellation rationale (superseded, naming what supersedes it; or not-required, naming why) and the story is not already terminal. Cancellation is orthogonal to the forward ## Workflow, so the target is always cancelled. Emits {decision, notes} JSON.
---

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

**On accept** — two `ledger_append` calls (no document_upsert):

```sh
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"review_accept","body":"<your notes>","payload":{"from_status":"<story_status>","to_status":"cancelled","gate":"satellites-story-cancel-review"}}'
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"status_transition","body":"<story_status> → cancelled","payload":{"from_status":"<story_status>","to_status":"cancelled"}}'
```

The status_transition row IS the status change; the status field on document_upsert is ignored.

**On reject** — record only the rejection, no status_transition:

```sh
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"review_reject","body":"<your notes>","payload":{"from_status":"<story_status>","gate":"satellites-story-cancel-review"}}'
```

If the status_transition `ledger_append` fails, the cancellation did not land — print `reject` with the failure as the reason.

## Output

After enacting, print exactly one JSON object and nothing else — no prose, no fence:

```json
{"decision": "accept", "notes": "one or two sentences of rationale"}
```

`decision` is `accept` or `reject`. On reject, `notes` must name the specific gap — already-terminal, or missing/vague rationale.
