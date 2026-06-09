---
name: satellites-story-start-review
type: skill
kind: gate
when: status==ready
tags: [kind:gate]
description: Gate skill for the start-work transition (ready → in_progress). Decides whether a story is genuinely ready for an executor to begin — plan accepted, no open blockers. Emits {decision, notes} JSON.
---

Decide whether the conditions to START work are met. This is a lightweight readiness gate, not a re-review of the plan (plan-review already accepted it to reach `ready`). Do not run the build or tests, and do not re-litigate plan quality.

## Input

One JSON object on stdin carrying `story_id`, `project_id`, `workspace_id`, `story_status` (current state), and `story_body` (markdown containing a `## Workflow` fenced yaml block). No `next_status` — resolve the target yourself (see *Enact*).

The gate's `.satellites/satellites exec` calls authenticate as the operator's admin user, authorized to write status_transition / review_* rows. Read related rows with `.satellites/satellites exec document_get` / `ledger_list` when the body is not enough.

## What to check

Accept when:
- The story still carries a `## Workflow` section — the pinned contract is intact.
- No **open blocker** — no `blocked-by:<sty_id>` tag whose target story is still un-`done`. Read the named blocker via the CLI; if not `done`, reject.
- The story still has a plan (Purpose / Approach / Acceptance criteria) — a sanity check it was not emptied.

Reject when the `## Workflow` or plan has gone missing, or a blocker is open.

## Enact

You enact your decision, you do not just report it.

Resolve your target from the story's `## Workflow`: parse its `transitions`, find the one whose `from == story_status` AND `reviewer_skill == satellites-story-start-review` (this gate's own name); its `to` is your `to_status`. If no such transition exists, reject (append `review_reject` below, print reject). Never invent a `to_status`.

Run these with Bash before printing your decision.

**On accept** — two `ledger_append` calls (no document_upsert):

```sh
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"review_accept","body":"<your notes>","payload":{"from_status":"<story_status>","to_status":"<to_status>","gate":"satellites-story-start-review"}}'
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"status_transition","body":"<story_status> → <to_status>","payload":{"from_status":"<story_status>","to_status":"<to_status>"}}'
```

The status_transition row IS the status change; the status field on document_upsert is ignored.

**On reject** — record only the rejection, no status_transition:

```sh
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"review_reject","body":"<your notes>","payload":{"from_status":"<story_status>","gate":"satellites-story-start-review"}}'
```

If the status_transition `ledger_append` fails, the transition did not land — print `reject` with the failure as the reason.

## Output

After enacting, print exactly one JSON object and nothing else — no prose, no fence:

```json
{"decision": "accept", "notes": "one or two sentences of rationale"}
```

`decision` is `accept` or `reject`. On reject, `notes` must name the specific reason the story is not ready to start.
