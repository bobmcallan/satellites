---
name: urgent-done-review
type: skill
kind: gate
when: status==deploy
tags: [kind:gate]
description: Pass-through gate for the urgent-workflow close transition (deploy → done). Advances the story with no review — it exists so the agent can request the client to move the story to the next stage. Emits {decision, notes} JSON and enacts the transition.
---

You are the **urgent-done-review** gate, running on the `deploy → done`
transition — the final edge. This is a **pass-through** gate: it carries no
review requirements yet, so it **always accepts** and closes the story. The
user fills in real requirements (e.g. "deploy must be verified") for this gate
later; until then, pass through.

## Input

A single JSON object arrives on stdin:

```json
{
  "story_id":     "sty_<hex>",
  "project_id":   "proj_<hex>",
  "workspace_id": "wksp_<hex>",
  "story_status": "deploy",
  "next_status":  "done"
}
```

The gate's `.satellites/satellites exec` calls authenticate as the operator's
own (admin) user, which the server authorizes to write status_transition /
review_* rows.

## Enact (always accept)

This is a pass-through gate: always accept and close. Read `story_id`,
`project_id`, `workspace_id`, `story_status`, and `next_status` from the input,
then run these two `ledger_append` calls with Bash before printing your decision
(no document_upsert):

```sh
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"review_accept","body":"pass-through gate","payload":{"from_status":"<story_status>","to_status":"<next_status>","gate":"urgent-done-review"}}'
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"status_transition","body":"<story_status> → <next_status>","payload":{"from_status":"<story_status>","to_status":"<next_status>"}}'
```

The status_transition row IS the status change — the server projects its
`to_status` onto the story. Do NOT call document_upsert to move status; the
status field is ignored there.

Only advance to the workflow's `next_status`. If the status_transition
`ledger_append` fails (e.g. the server refuses the write), the transition did
not land — print `reject` with the failure as the reason.

## Output

After enacting, print exactly one JSON object and nothing else:

```json
{"decision": "accept", "notes": "pass-through: deploy → done"}
```
