---
name: satellites-task-upsert-review
type: skill
kind: reviewer
when: status==ready||status==complete
tags: [kind:reviewer]
description: The default TASK entry/definition gate — the "task_reviewer". Judges that a task is WELL-FORMED — its body declares the ACTION (what work), the OUTPUT (the deliverable AND its concrete output location), and the VERIFICATION (how success is judged) — the structural contract that lets the exit gate judge the output — and is a RE-RUNNABLE definition (rejecting a story-shaped/literal body — past-tense narration, Purpose/Approach/AC scaffolding, or an embedded story workflow section) and carries a resolvable governing workflow, and only then opens it for execution by moving it to running (ready → running, or complete → running on a RE-RUN — each open begins a fresh execution episode). A skill:<name> reference is the agent's Claude capability, surfaced as a warning — never resolved or gated. The entry gate of satellites-task-workflow, the sibling of the exit gate satellites-task-report-review. Pure judgment, no functional check. Emits {decision, notes} JSON.
---

Decide ONE thing: is this document a **well-formed, executable task**, ready to
be opened for execution? This is the task lifecycle's entry gate — the
"task_reviewer". It checks the document IS a task (not a half-formed note) whose
body declares its ACTION, OUTPUT, and VERIFICATION, with a resolvable governing
workflow, before it moves the task to `running` — whether opening it the first time
(`ready → running`) or re-running a completed task (`complete → running`, a fresh
execution episode). It does NOT judge whether the work is done — that is the exit
gate satellites-task-report-review.

## Input

One JSON object on stdin carrying `story_id` (the task's `tsk_` id),
`project_id`, `workspace_id`, `story_status` (current state — `ready`) and
`story_body` (the task markdown). The governing workflow is resolved BY REFERENCE
from the task's `workflow:` selector or its category default — the body need NOT
carry a `## Workflow` block (reference-not-copy); when present it is display only.
No `next_status` — the client resolves the target from that workflow on your
accept. The gate READS this context and JUDGES only — it writes nothing; the
client enacts your verdict.

## Decision rule

Judge the `story_body`:

- **accept** — the document is a genuine, well-formed task. Its body declares,
  clearly enough for the in-repo agent to act on:
  - **ACTION** — WHAT work to perform (e.g. "scan the tracked files for committed
    secrets and produce a findings report");
  - **OUTPUT** — the deliverable the run produces AND its concrete LOCATION: a
    definite path/dir the run writes to (e.g. `docs/reports/<name>-<date>.md`), not
    merely "a report". A declared output location keeps the executor from guessing
    where to write and makes two runs of the same task agree on where output lands;
  - **VERIFICATION** — how success is judged (a stated verdict, a check, a
    measurable signal). This is what the exit gate later judges the output
    against, so it MUST be present.

  And it carries a resolvable governing workflow (a valid `workflow:` selector, or
  a category that resolves a default — see *Enact*; the governing workflow has a
  transition whose `from == story_status` and
  `reviewer_skill == satellites-task-upsert-review`). The task is genuinely ready
  to be executed by the agent.
- **reject** — the body is empty, a stub, or does not describe executable work; or
  it is missing any of ACTION / OUTPUT / VERIFICATION (name which) — including an
  OUTPUT that names no concrete output location (path/dir) the run writes to; or the
  goal is too vague for the agent to act on; or it is STORY-SHAPED rather than a re-runnable
  task definition (see *Structure*); or no governing-workflow transition matches
  this gate from the current status. Name exactly what is missing.

## Structure — a task is a RE-RUNNABLE definition, not a story

This is the priority check. A task body states work to be performed AGAIN and
judged per run — NOT a narrative of a one-off already done. Reject a STORY-SHAPED
body even when its `type` is `task`, naming the structural defect:

- **Past-tense / one-off narration** — "Ran X, produced the report at <path>, found
  N issues" describes a completed deliverable, not a repeatable action.
  ACTION / OUTPUT / VERIFICATION must read in the present/imperative: *each run*
  performs the ACTION → emits the OUTPUT → judged by the VERIFICATION.
- **Story scaffolding copied in** — a body built around Purpose / Approach /
  numbered acceptance-criteria (the STORY contract) is a story typed as a task. A
  task's contract is ACTION + OUTPUT + VERIFICATION, not story ACs.
- **Wrong embedded workflow section** — if the body carries a `## Workflow` block it
  MUST be the task lifecycle (`ready → running → complete`). A story workflow
  (`backlog → in_progress → done`) embedded in a task is a sure tell the content was
  copied from a story — reject as wrong-structure, naming it. (The governing
  workflow is still resolved BY REFERENCE; this is a structural tell, not the
  authority — consistent with reference-not-copy.)

On reject, name the structural defect (story-shaped narration, story ACs, or a
story workflow section) and tell the author to RE-AUTHOR the body as a re-runnable
ACTION / OUTPUT / VERIFICATION definition — converting a story to a task is a
re-authoring, not a type-swap.

## A referenced skill is a warning, not a gate

A task MAY name a skill for the agent to use (a `skill:<name>` tag). That skill is
the agent's Claude capability in its own environment — NOT satellites substrate to
resolve or govern. Do NOT resolve it, and NEVER reject on it. When present, NOTE
it in your accept notes as context for the executing agent (e.g. "uses
skill:<name> — ensure it is available locally"). The task body itself is the work
definition the agent acts on.

Fail closed: if the body cannot be read or the task cannot be judged, reject with
the reason named.

## Environment

You are a reviewer. You read the task body and judge it; you write NOTHING — no
ledger rows, no `document_upsert`, no git/file mutation. The client enacts your verdict.

```yaml
guardrails:
  always:
    - Judge whether the task's body declares ACTION + OUTPUT (with a concrete output LOCATION — a path/dir the run writes to) + VERIFICATION with a resolvable governing workflow; reject naming any missing element.
    - Reject a STORY-SHAPED body — past-tense/one-off narration, Purpose/Approach/AC story scaffolding, or an embedded story workflow section (backlog→in_progress→done). A task is a re-runnable ACTION/OUTPUT/VERIFICATION definition, not a story typed as a task; tell the author to re-author it.
    - A skill:<name> reference is the agent's Claude capability — note it as a warning in the accept notes; NEVER resolve it or reject on it.
    - Verify the GOVERNING workflow (the task's workflow: selector, else the category default) declares a transition whose from == story_status AND reviewer_skill == satellites-task-upsert-review — if none exists, reject; the client enacts that edge on your accept.
    - Fail closed — if the task body cannot be read or judged, print reject with the reason named.
    - The gate writes nothing — emit exactly one JSON object {decision, notes} as its only output.
  ask_first: []
  never:
    - Judge whether the work is COMPLETE — that is the exit gate satellites-task-report-review.
    - Accept when the governing workflow declares no matching transition from this status for this gate.
    - Write anything at all — no ledger_append, no document_upsert, no other mutating exec. The gate is judge-only; the client enacts.
```

## Output

Print exactly one JSON object and nothing else — no prose, no fence:

```json
{"decision": "accept", "notes": "one or two sentences; on reject, name exactly what makes this not a well-formed/ready task"}
```

`decision` is `accept` or `reject`.
