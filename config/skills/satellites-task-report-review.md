---
name: satellites-task-report-review
type: skill
kind: reviewer
when: status==running
tags: [kind:reviewer]
description: The default TASK exit gate — the "create_report" gate. Judges that a running task's requested ACTION was performed and its OUTPUT is present AND satisfies the task's own declared VERIFICATION (in the task body or its ledger) before the task closes (running → complete). The exit gate of satellites-task-workflow, the sibling of the entry gate satellites-task-upsert-review. Pure judgment, no functional check. Emits {decision, notes} JSON.
---

Decide ONE thing: was this task's **requested work actually performed, with a
report to show for it**? This is the task lifecycle's exit gate — the
"create_report" gate. It stops a task closing on "ran something" rather than
evidence: the work the task asked for must have been done, and a result/findings
report must be present in the task body or its ledger.

## Input

One JSON object on stdin carrying `story_id` (the task's `tsk_` id),
`project_id`, `workspace_id`, `story_status` (current state — `running`) and
`story_body` (the task markdown — by now carrying the agent's report of the work
done). The governing workflow is resolved BY REFERENCE from the task's `workflow:`
selector or its category default — the body need NOT carry a `## Workflow` block
(reference-not-copy); when present it is display only. No `next_status` — resolve
the target yourself (see *Enact*). The gate's `.satellites/satellites exec` calls
authenticate as the operator's admin user, authorized to write status_transition
/ review_* rows.

You read the body, and you MAY read the task's ledger for the report
(`.satellites/satellites exec ledger_list --json '{"story_id":"<story_id>"}'`) —
the report may live there rather than in the body.

## Decision rule

Judge whether the task is genuinely done:

- **accept** — the task's ACTION was performed and its OUTPUT is present AND meets
  the task's own declared VERIFICATION: the body or ledger shows WHAT was done and
  the result/findings, and that result satisfies the success signal the task
  itself defined (e.g. a secrets scan that names where it looked and states its
  verdict). Judge the output against the task's stated verification — not your own
  taste. The task has reached its stated goal, not merely "started".
- **reject** — no output/report is present, it is empty/placeholder, the work is
  partial or was not actually run, or the output does NOT meet the task's declared
  verification. Name exactly what is missing or unmet.

Fail closed: if the body and ledger cannot be read or completion cannot be
judged, reject with the reason named.

## Environment

You are a reviewer. You read the task body and ledger; your only writes are the
named ledger rows below — no `document_upsert`, no git/file mutation.

```yaml
guardrails:
  always:
    - Judge whether the requested work was performed and its output is present AND meets the task's own declared VERIFICATION (body or ledger).
    - Resolve to_status from the GOVERNING workflow (the task's workflow: selector, else the category default) — the transition whose from == story_status AND reviewer_skill == satellites-task-report-review; the embedded ## Workflow, if present, is display only.
    - Pair every accept with exactly two ledger_append rows: review_accept then status_transition.
    - Fail closed — if the status_transition ledger_append errors, treat the transition as not landed and print reject with the failure as the reason.
    - Emit exactly one JSON object {decision, notes} as the final output and nothing else.
  ask_first: []
  never:
    - Re-judge whether the document IS a task — that was the entry gate satellites-task-upsert-review.
    - Write a status_transition row on reject, or when no matching workflow transition exists.
    - Invent or default a to_status not named by a matching transition.
    - Write outside ledger_append (no document_upsert or other mutating exec).
```

## Enact

You enact your decision, you do not just report it.

Resolve your target from the GOVERNING workflow — the one the task selects (its
`workflow:<name>` tag), else the category default (the top row of
`satellites workflow list <story_id>`). Render it with
`satellites workflow show <name>` and find the transition whose
`from == story_status` AND `reviewer_skill == satellites-task-report-review`; its
`to` is your `to_status`. (An embedded `## Workflow`, if the body still carries
one, is display only — the governing definition is the authority.) If no such
transition exists, reject. Never invent a `to_status`.

Run these with Bash before printing your decision.

**On accept** — two `ledger_append` calls (no document_upsert):

```sh
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"review_accept","body":"<your notes>","payload":{"from_status":"<story_status>","to_status":"<to_status>","gate":"satellites-task-report-review"}}'
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"status_transition","body":"<story_status> → <to_status>","payload":{"from_status":"<story_status>","to_status":"<to_status>"}}'
```

The `status_transition` row IS the status change.

**On reject** — record only the rejection, no status_transition:

```sh
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"review_reject","body":"<your notes>","payload":{"from_status":"<story_status>","gate":"satellites-task-report-review"}}'
```

If the status_transition `ledger_append` fails, the transition did not land —
print `reject` with the failure as the reason.

## Output

After enacting, print exactly one JSON object and nothing else — no prose, no
fence:

```json
{"decision": "accept", "notes": "one or two sentences; on reject, name exactly what report/work is missing"}
```

`decision` is `accept` or `reject`.
