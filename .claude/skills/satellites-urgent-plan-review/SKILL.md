---
name: urgent-plan-review
type: skill
kind: gate
when: status==backlog
tags: [kind:gate]
description: Pass-through gate for the urgent-workflow entry transition (plan → in-progress). Resolves its target from the story's ## Workflow, always accepts, and enacts the status_transition. Emits {decision, notes} JSON.
---
<!-- satellites-sync:begin {"document_id":"doc_d8565cd6","version":6,"hash":"cc01a906901c73534a8cb0768fbe428e686b2d45e0b1ebe6fa02dfa1f92558f1"} satellites-sync:end -->

## Enact (always accept)

Read one JSON object from stdin carrying `story_id`, `project_id`, `workspace_id`, `story_status` (current state), and `story_body` (markdown containing a `## Workflow` fenced yaml block). There is no `next_status` on input — even a pass-through gate resolves the target itself.

Resolve your `to_status` from the story's `## Workflow`: parse its `transitions`, find the one whose `from == story_status` AND `reviewer_skill == satellites-urgent-plan-review`, and take its `to`. The `satellites-` prefix is the platform's upload-time namespacing of this skill's `name` (`urgent-plan-review`); match the prefixed form, because that is what the story's `reviewer_skill` field carries. If no such transition exists, reject (append `review_reject`, print `reject`). Never invent a `to_status`.

Then run these two `ledger_append` calls with Bash before printing your decision:

```sh
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"review_accept","body":"pass-through gate","payload":{"from_status":"<story_status>","to_status":"<to_status>","gate":"satellites-urgent-plan-review"}}'
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"status_transition","body":"<story_status> → <to_status>","payload":{"from_status":"<story_status>","to_status":"<to_status>"}}'
```

The status_transition row IS the status change. If the status_transition `ledger_append` fails, the transition did not land — print `reject` with the failure as the reason.

After enacting, print exactly one JSON object and nothing else:

```json
{"decision": "accept", "notes": "pass-through: <story_status> → <to_status>"}
```

## Decision rule

Always accept when a matching `## Workflow` transition exists. This gate carries no review requirements; its only job is to resolve the declared target, record the transition, and advance the story. Reject only when no matching transition exists or the `status_transition` write fails.

## Environment

The gate's `.satellites/satellites exec` calls authenticate as the operator's admin user, authorized to write `status_transition` and `review_*` rows. It reads stdin and the story body; it writes exactly two ledger rows per accept.

```yaml
guardrails:
  always:
    - Resolve to_status from the ## Workflow transition where from == story_status AND reviewer_skill == satellites-urgent-plan-review.
    - Append review_accept, then status_transition, before printing the decision.
    - Print exactly one JSON object {decision, notes} and nothing else.
  ask_first: []
  never:
    - Invent or hard-code a to_status not present in the story's ## Workflow.
    - Write document_upsert or any ledger kind other than review_accept / review_reject / status_transition.
    - Print accept when the status_transition ledger_append failed — reject with the failure as the reason.
```
