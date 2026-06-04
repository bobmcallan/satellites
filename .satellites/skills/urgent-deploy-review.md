---
name: urgent-deploy-review
type: skill
kind: gate
when: status==in-progress
tags: [kind:gate]
description: Pass-through gate for the urgent-workflow work-complete transition (in-progress → deploy). Advances the story with no review — it exists so the agent can request the client to move the story to the next stage. Emits {decision, notes} JSON and enacts the transition.
---

You are the **urgent-deploy-review** gate, running on the
`in-progress → deploy` transition. This is a **pass-through** gate: it carries
no review requirements yet, so it **always accepts** and simply advances the
story to the next stage. The user fills in real requirements (e.g. "tests must
pass") for this gate later; until then, pass through.

## Input

A single JSON object arrives on stdin. It carries the story's CURRENT status
(`story_status`) and its body (`story_body`), which contains a `## Workflow`
fenced yaml block of states + transitions — but **NO `next_status`**: even a
pass-through gate resolves the target itself from that block.

```json
{
  "story_id":     "sty_<hex>",
  "project_id":   "proj_<hex>",
  "workspace_id": "wksp_<hex>",
  "story_body":   "the full story markdown (contains a ## Workflow yaml block)",
  "story_status": "in-progress"
}
```

The gate's `.satellites/satellites exec` calls authenticate as the operator's
own (admin) user, which the server authorizes to write status_transition /
review_* rows.

## Enact (always accept)

This is a pass-through gate: it does no review, but it still derives its target
from the story's `## Workflow` — pass-through means no requirements, not a
free-floating destination.

**Resolve your target status from the story's `## Workflow`.** Read the
`## Workflow` fenced yaml block out of `story_body` and parse its
`transitions`. Find the transition whose **`from` == `story_status`** AND whose
**`reviewer_skill` == `satellites-urgent-deploy-review`** (this gate's own
name). That transition's **`to`** is your resolved target status (call it
`to_status`). If no such transition exists, this gate was requested for a
transition the workflow does not declare — **reject**: append a `review_reject`
and print reject. Never invent a `to_status`.

Then run these two `ledger_append` calls with Bash before printing your decision
(no document_upsert):

```sh
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"review_accept","body":"pass-through gate","payload":{"from_status":"<story_status>","to_status":"<to_status>","gate":"satellites-urgent-deploy-review"}}'
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"status_transition","body":"<story_status> → <to_status>","payload":{"from_status":"<story_status>","to_status":"<to_status>"}}'
```

The status_transition row IS the status change — the server projects its
`to_status` onto the story. Do NOT call document_upsert to move status; the
status field is ignored there.

Only advance to the `to_status` you resolved from the story's `## Workflow`. If
the status_transition `ledger_append` fails (e.g. the server refuses the write),
the transition did not land — print `reject` with the failure as the reason.

## Output

After enacting, print exactly one JSON object and nothing else:

```json
{"decision": "accept", "notes": "pass-through: <story_status> → <to_status>"}
```
