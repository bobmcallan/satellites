---
name: urgent-deploy-review
type: skill
kind: gate
when: status==in-progress
tags: [kind:gate]
description: Pass-through gate for the urgent-workflow work-complete transition (in-progress → deploy). Advances the story with no review — it exists so the agent can request the client to move the story to the next stage. Emits {decision, notes} JSON and enacts the transition.
---

This is a pass-through gate: it carries no review requirements, so it always accepts and advances the story. It exists to give the agent one way to ask the client to move the story forward and to leave a ledger record of each transition.

## Input

One JSON object on stdin carrying `story_id`, `project_id`, `workspace_id`, `story_status` (current state), and `story_body` (markdown containing a `## Workflow` fenced yaml block). No `next_status` — even a pass-through gate resolves the target itself.

The gate's `.satellites/satellites exec` calls authenticate as the operator's admin user, authorized to write status_transition / review_* rows.

## Enact (always accept)

Resolve your target from the story's `## Workflow`: parse its `transitions`, find the one whose `from == story_status` AND `reviewer_skill == satellites-urgent-deploy-review` (this gate's own name); its `to` is your `to_status`. If no such transition exists, reject (append `review_reject`, print reject). Never invent a `to_status`.

Then run these two `ledger_append` calls with Bash before printing your decision (no document_upsert):

```sh
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"review_accept","body":"pass-through gate","payload":{"from_status":"<story_status>","to_status":"<to_status>","gate":"satellites-urgent-deploy-review"}}'
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"status_transition","body":"<story_status> → <to_status>","payload":{"from_status":"<story_status>","to_status":"<to_status>"}}'
```

The status_transition row IS the status change; the status field on document_upsert is ignored. If the status_transition `ledger_append` fails, the transition did not land — print `reject` with the failure as the reason.

## Output

After enacting, print exactly one JSON object and nothing else:

```json
{"decision": "accept", "notes": "pass-through: <story_status> → <to_status>"}
```
