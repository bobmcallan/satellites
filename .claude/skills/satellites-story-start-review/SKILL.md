---
name: satellites-story-start-review
type: skill
kind: gate
when: status==ready
tags: [kind:gate]
description: Gate skill for the start-work transition (ready → in_progress). Decides whether a story is genuinely ready for an executor to begin — plan accepted, no open blockers. Emits {decision, notes} JSON.
---
<!-- satellites-sync:begin {"document_id":"doc_e176db0f","version":1,"hash":"6a642f2d785664aea6cd8fa455d13be2269c6e32a164501a78de98ba37155a0a"} satellites-sync:end -->

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

Resolve your target from the story's `## Workflow`: parse its `transitions`, find the one whose `from == story_status` AND `reviewer_skill == satellites-story-start-review` (this gate's own name); its `to` is your `to_status`. If no such transition exists, reject (append `review_reject` below, print reject).

Run these with Bash before printing your decision.

**On accept** — two `ledger_append` calls:

```sh
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"review_accept","body":"<your notes>","payload":{"from_status":"<story_status>","to_status":"<to_status>","gate":"satellites-story-start-review"}}'
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"status_transition","body":"<story_status> → <to_status>","payload":{"from_status":"<story_status>","to_status":"<to_status>"}}'
```

The status_transition row IS the status change.

**On reject** — record only the rejection, no status_transition:

```sh
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"review_reject","body":"<your notes>","payload":{"from_status":"<story_status>","gate":"satellites-story-start-review"}}'
```

If the status_transition `ledger_append` fails, the transition did not land — print `reject` with the failure as the reason.

## Environment

Runs as a one-shot gate over a single story's body and ledger, writing `review_*` / `status_transition` rows through the `satellites` CLI as the operator's admin user. Tool use is bounded by:

```yaml
guardrails:
  always:
    - Resolve to_status only from the matching `## Workflow` transition (from == story_status && reviewer_skill == self).
    - On accept, append BOTH a review_accept and a status_transition row — the status_transition row is the status change.
    - On reject, name the specific reason the story is not ready in `notes`.
    - Treat a failed status_transition append as a non-transition and print reject with the failure as the reason.
    - Print exactly one JSON object and nothing else.
  ask_first: []
  never:
    - Run the build or tests, or re-litigate plan quality (plan-review already accepted it).
    - Invent or hardcode a to_status not present in the story's `## Workflow` transitions.
    - Append a status_transition row on reject.
    - Write any row for a story other than the one on stdin.
```

This gate is its own verifier; it has no separate human approval step, so `ask_first` is empty.

## Output

After enacting, print exactly one JSON object and nothing else — no prose, no fence:

```json
{"decision": "accept", "notes": "one or two sentences of rationale"}
```

`decision` is `accept` or `reject`. On reject, `notes` must name the specific reason the story is not ready to start.
