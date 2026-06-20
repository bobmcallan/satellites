---
name: satellites-task-workflow
scope: system
kind: workflow
tags: [kind:workflow]
applies_to: ["task"]
description: The default lifecycle for a top-level project TASK (type:task, tsk_; epic:project-tasks) — ready to running to complete, reviewer-gated, with cancel edges. A task is a re-runnable, reviewer-checked unit of work the in-repo agent is asked to execute; the reviewer is the ONLY status-updater. Two forward gates bracket it — satellites-task-upsert-review opens it (it judges the document IS a well-formed task and records a resolvable governing workflow), and satellites-task-report-review closes it (it judges the requested work was performed and a report is present). Cancel edges are gated by satellites-task-cancel-review. All three gates are system substrate resolved from the binary embed. A task selects this workflow by category='task'; a specific task may override via a workflow selector tag, the same mechanism as stories.
---

# Task workflow (reviewers-only)

The default governing workflow for every top-level task. A task moves
`ready → running → complete`; the reviewer is the single enforcement primitive
(see [[reviewer-only-model]]) — a `claude -p` judgment that enacts the
transition by recording a `status_transition` ledger row, which the agent cannot
bypass. This workflow names only reviewers that ship in the binary embed, so
every gated edge resolves.

The operator's flow — `task → task_reviewer (format & workflow) → running →
create_report → complete`:

- the **entry** (`ready → running`) is judged by `satellites-task-upsert-review`
  (the *task_reviewer*): it checks the document is a well-formed, executable task
  with a resolvable governing workflow, then opens it for execution; and
- the **exit** (`running → complete`) is judged by `satellites-task-report-review`
  (the *create_report* gate): it checks the requested work was actually performed
  and a findings/result report is present in the body or ledger before the task
  closes.

The agent DRIVES the work while the task is `running` — it may perform a step by
invoking a project `.claude/skill` (e.g. a scan skill) or do the work inline. The
reviewer never does the work and never moves status except through its gate.

The cancel edges are gated by `satellites-task-cancel-review`, which accepts a
move to `cancelled` only on a concrete `## Cancellation` rationale.

**Re-runnable.** A task is *executed multiple times*: a `complete` task can be
re-run — `complete → running`, re-validated by `satellites-task-upsert-review` —
opening a fresh **execution episode**. Each episode is the `→running … →complete`
span recorded in the append-only ledger, so re-running never disturbs a prior
run's rows; `satellites task executions <tsk-id>` lists every episode oldest-first
with its start/end timestamps. The task body holds the latest run's context; the
ledger holds them all.

## Executing a task

"Implement `tsk_…`" is this loop — the agent (the `running` executor) drives the
task to `complete`:

1. **Resolve** — `document_get` the task (`satellites task get --body <tsk-id>`):
   read its body (the work to do) and its tags.
2. **Embed** — `satellites workflow embed <tsk-id>` stamps this workflow's
   `## Workflow` into the task so the gates can resolve their targets.
3. **Open** — `satellites task status_transition --skill satellites-task-upsert-review <tsk-id>`
   (ready → running, or complete → running to re-run).
4. **Work (at `running`, the executor)** — do the work the body describes. If the
   task carries a **`skill:<name>` tag**, perform the work by running that skill
   (the Skill tool / `.claude/skills/<name>`); when there is no `skill:` tag, do
   the work inline. The `skill:` tag is the structured, resolvable bind from the
   task to its work skill — prefer it over any prose mention.
5. **Report** — record the result in the task body (a `## Report` section) or the
   ledger; that is what the exit gate reads.
6. **Close** — `satellites task status_transition --skill satellites-task-report-review <tsk-id>`
   (running → complete). Re-run any time by repeating from step 3.

The executor RUNS the skill and writes the report; it never moves the status — a
reviewer gate is the sole status-updater (see [[reviewer-only-model]]).

## Workflow

- ready to running — open for execution, gated by satellites-task-upsert-review (it IS a task + a resolvable workflow).
- running to complete — close, gated by satellites-task-report-review (the work was done and a report is present).
- complete to running — RE-RUN (a fresh execution episode), gated by satellites-task-upsert-review (re-validates the task).
- ready/running to cancelled — abandon, gated by satellites-task-cancel-review (concrete cancellation rationale).

```yaml
states:
  - ready
  - {name: running, actor: executor}
  - complete
  - cancelled
transitions:
  - {from: ready, to: running, reviewer_skill: "satellites-task-upsert-review"}
  - {from: running, to: complete, reviewer_skill: "satellites-task-report-review"}
  - {from: complete, to: running, reviewer_skill: "satellites-task-upsert-review"}
  - {from: ready, to: cancelled, reviewer_skill: "satellites-task-cancel-review"}
  - {from: running, to: cancelled, reviewer_skill: "satellites-task-cancel-review"}
```

## Environment

Drives a task document ready → running → complete. The entry is gated by the
task_reviewer; the exit is gated by the report reviewer; the cancels are gated by
the cancel reviewer. Only a reviewer's accept moves the status.

```yaml
guardrails:
  always:
    - Drive the engaged task to a terminal state (complete) — do the work while running, then request the report gate.
    - Request satellites-task-upsert-review to open a task (ready → running); it checks the document is a well-formed task with a resolvable workflow.
    - Perform the task's work while running by RUNNING the skill named in the task's `skill:<name>` tag (the Skill tool / .claude/skills/<name>); when the task has no `skill:` tag, do the work inline. Record a report (in the body or the ledger) before requesting the exit.
    - Request satellites-task-report-review to close a task (running → complete); it checks the work was done and a report is present.
    - To abandon a task, request satellites-task-cancel-review with a concrete ## Cancellation rationale — never set-status to cancelled.
  ask_first: []
  never:
    - Move a task across a gated edge with set-status — route it through the named gate.
    - Move a task across an edge this workflow does not declare.
```
