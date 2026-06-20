---
name: satellites-parent-cancel-review
type: skill
kind: reviewer
when: cancel-requested
tags: [kind:reviewer]
description: The default EPIC/parent cancel gate — accepts a move to the terminal status `cancelled` for an anchor (epic/parent) story only when it is non-terminal and its body carries a concrete `## Cancellation` rationale (superseded by a named epic/story, or not-required with a stated reason). The sibling of satellites-parent-close-review (the DONE gate); cancellation is orthogonal to the close contract, so it does NOT require children to be terminal — the abandoning operator handles open children separately. Target is fixed (`cancelled`). Emits {decision, notes} JSON.
---

Decide whether an anchor (epic / parent) story's cancellation is justified, and
if so enact it by moving the story to the terminal status `cancelled`.
Cancellation is orthogonal to the forward workflow and to the close contract — do
NOT resolve a target from `## Workflow`, and do NOT require children to be
terminal (that is the DONE gate, satellites-parent-close-review). The target is
fixed: `to_status` is always `cancelled`, `from_status` is the story's current
`story_status`.

## Input

One JSON object on stdin carrying `story_id`, `project_id`, `workspace_id`,
`story_status` (current state), and `story_body` (the full anchor markdown).

The gate's `.satellites/satellites exec` calls authenticate as the operator's
admin user, authorized to write status_transition / review_* rows.

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

## Enact

You enact your decision, you do not just report it. Your target is fixed:
`to_status = "cancelled"`, `from_status = story_status`. Do NOT read `## Workflow`
— cancellation is orthogonal. Run these with Bash before printing your decision.

**On accept** — two `ledger_append` calls:

```sh
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"review_accept","body":"<your notes>","payload":{"from_status":"<story_status>","to_status":"cancelled","gate":"satellites-parent-cancel-review"}}'
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"status_transition","body":"<story_status> → cancelled","payload":{"from_status":"<story_status>","to_status":"cancelled"}}'
```

The `status_transition` row IS the status change.

**On reject** — record only the rejection, no status_transition:

```sh
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"review_reject","body":"<your notes>","payload":{"from_status":"<story_status>","gate":"satellites-parent-cancel-review"}}'
```

If the status_transition `ledger_append` fails, the cancellation did not land —
print `reject` with the failure as the reason.

## Environment

Runs as a cancel gate over one anchor story's JSON on stdin, writing to the
ledger via `.satellites/satellites exec` under the operator's admin auth. It
mutates external state (review rows and one status transition), so its tool use
is bounded by the guardrails below.

```yaml
guardrails:
  always:
    - Target is fixed — to_status=cancelled, from_status=story_status; never resolve a target from `## Workflow`.
    - On accept, write both ledger_append rows (review_accept then status_transition); the status_transition row is what enacts the change.
    - Verify the status_transition ledger_append succeeded before printing accept; on its failure print reject with the failure as the reason.
    - Emit exactly one {decision, notes} JSON object as the only output.
  ask_first: []
  never:
    - Never call document_upsert — this gate does not edit the anchor body.
    - Never write a status_transition row on reject.
    - Never accept an anchor whose story_status is already terminal (done or cancelled).
    - Never accept on a vague or missing cancellation rationale.
```

## Output

After enacting, print exactly one JSON object and nothing else — no prose, no
fence:

```json
{"decision": "accept", "notes": "one or two sentences of rationale"}
```

`decision` is `accept` or `reject`. On reject, `notes` must name the specific gap
— already-terminal, or missing/vague rationale.
