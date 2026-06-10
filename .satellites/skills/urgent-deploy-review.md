---
name: urgent-deploy-review
type: skill
kind: gate
when: status==in-progress
tags: [kind:gate]
description: Pass-through gate for the urgent-workflow work-complete transition (in-progress → deploy). Advances the story with no review — it exists so the agent can request the client to move the story to the next stage. Emits {decision, notes} JSON and enacts the transition.
---

Always accept and advance the story. This gate carries no review requirements; it exists so the agent can request the next stage and leave a ledger record of each transition.

This gate's name on the substrate is the frontmatter `name` prefixed with `satellites-` — i.e. `satellites-urgent-deploy-review`. Use that fully-qualified form everywhere a `reviewer_skill` or `gate` value is matched or written below.

## Input

One JSON object on stdin carrying `story_id`, `project_id`, `workspace_id`, `story_status` (current state), and `story_body` (markdown containing a `## Workflow` fenced yaml block). No `next_status` — even a pass-through gate resolves the target itself.

The gate's `.satellites/satellites exec` calls authenticate as the operator's admin user, authorized to write status_transition / review_* rows.

## Enact (always accept)

1. Resolve the target from the story's `## Workflow`: parse its `transitions`, find the one whose `from == story_status` AND `reviewer_skill == satellites-urgent-deploy-review`; its `to` is your `to_status`.
2. If no such transition exists, reject — append `review_reject` and print the reject object. Never invent a `to_status`.
3. Otherwise run these two `ledger_append` calls with Bash, in order, before printing your decision:

```sh
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"review_accept","body":"pass-through gate","payload":{"from_status":"<story_status>","to_status":"<to_status>","gate":"satellites-urgent-deploy-review"}}'
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"status_transition","body":"<story_status> → <to_status>","payload":{"from_status":"<story_status>","to_status":"<to_status>"}}'
```

The `status_transition` row IS the status change. If the `status_transition` `ledger_append` fails, the transition did not land — print `reject` with the failure as the reason.

## Output

After enacting, print exactly one JSON object and nothing else:

```json
{"decision": "accept", "notes": "pass-through: <story_status> → <to_status>"}
```

## Environment

Runs as a stdin-driven gate, authenticated as the operator's admin user with authority to append ledger rows on the substrate.

```yaml
guardrails:
  always:
    - Resolve to_status only from a ## Workflow transition where from == story_status and reviewer_skill == satellites-urgent-deploy-review.
    - Append review_accept then status_transition, in that order, before printing accept.
    - Print exactly one JSON object as output and nothing else.
  ask_first: []
  never:
    - Invent or guess a to_status that is not in the resolved transition.
    - Call document_upsert or any tool other than ledger_append (the status field on document_upsert is ignored).
    - Print accept when the status_transition append failed — reject with the failure as the reason.
```
