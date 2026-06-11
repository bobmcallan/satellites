---
name: urgent-done-review
type: skill
kind: gate
when: status==deploy
tags: [kind:gate]
description: Pass-through gate for the urgent-workflow close transition (deploy → done). Advances the story with no review — it exists so the agent can request the client to move the story to the next stage. Emits {decision, notes} JSON and enacts the transition.
---
<!-- satellites-sync:begin {"document_id":"doc_2c9a7dd3","version":5,"hash":"ab0eaae2e5d96ee264ed0d4981932a376ef5cbdc4e7e7e6b7ce188fe49512fd9"} satellites-sync:end -->

Always accept and close the story: this gate carries no review requirements. Resolve the target state from the story's `## Workflow`, append the ledger rows that enact the move, then print the decision.

## Naming

On upload this skill is registered as `satellites-urgent-done-review` (the substrate prepends the `satellites-` prefix to the frontmatter `name`). That prefixed form is the value a story's `reviewer_skill` carries and the value you write into the `gate` payload field — use it exactly as written below.

## Input

One JSON object on stdin carrying `story_id`, `project_id`, `workspace_id`, `story_status` (current state), and `story_body` (markdown containing a `## Workflow` fenced yaml block). No `next_status` — even a pass-through gate resolves the target itself.

The gate's `.satellites/satellites exec` calls authenticate as the operator's admin user, authorized to write status_transition / review_* rows.

## Enact (always accept)

Resolve your target from the story's `## Workflow`: parse its `transitions`, find the one whose `from == story_status` AND `reviewer_skill == satellites-urgent-done-review`; its `to` is your `to_status`. If no such transition exists, reject (append `review_reject`, print reject). Never invent a `to_status`.

Then run these two `ledger_append` calls with Bash before printing your decision (no document_upsert):

```sh
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"review_accept","body":"pass-through gate","payload":{"from_status":"<story_status>","to_status":"<to_status>","gate":"satellites-urgent-done-review"}}'
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"status_transition","body":"<story_status> → <to_status>","payload":{"from_status":"<story_status>","to_status":"<to_status>"}}'
```

The status_transition row IS the status change; the status field on document_upsert is ignored. If the status_transition `ledger_append` fails, the transition did not land — print `reject` with the failure as the reason.

## Environment

Runs as the urgent-workflow close gate, fired when a story reaches `deploy`. It mutates substrate by appending ledger rows as the operator's admin user; it reads the story body but uploads no documents.

```yaml
guardrails:
  always:
    - Resolve to_status only from a matching ## Workflow transition (from == story_status AND reviewer_skill == satellites-urgent-done-review).
    - Append both review_accept and status_transition rows before printing accept.
    - Treat a failed status_transition append as a non-transition — print reject with the failure as the reason.
  ask_first: []
  never:
    - Invent, default, or guess a to_status when no matching transition exists — reject instead.
    - Write document_upsert rows or any ledger kind beyond review_accept / review_reject / status_transition.
    - Add review requirements or block the story — this gate is pass-through.
```

## Output

After enacting, print exactly one JSON object and nothing else:

```json
{"decision": "accept", "notes": "pass-through: <story_status> → <to_status>"}
```
