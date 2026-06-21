---
name: satellites-task-workflow
scope: system
kind: workflow
tags: [kind:workflow]
applies_to: ["task"]
description: The default lifecycle for a top-level project TASK (type task, tsk_; epic project-tasks) — ready to running to complete, reviewer-gated, with cancel edges. A task is a re-runnable, reviewer-checked unit of work the in-repo agent executes; the reviewer is the ONLY status-updater. Two forward gates bracket it — satellites-task-upsert-review opens it (judges it IS a well-formed task with a resolvable governing workflow) and satellites-task-report-review closes it (judges the work was performed and a report is present). Cancel edges are gated by satellites-task-cancel-review. All three gates are system substrate resolved from the binary embed. A task selects this workflow by category='task' (or a workflow selector tag), resolved by reference — no embedded ## Workflow copy is required.
---

# Task workflow (reviewers-only)

The default governing workflow for every top-level task. A task moves
`ready → running → complete`; the reviewer is the single enforcement primitive
(see [[reviewer-only-model]]) — a `claude -p` judgment that enacts the transition
by writing a `status_transition` ledger row the agent cannot bypass. Every gated
edge names a reviewer that ships in the binary embed, so it always resolves.

**Re-runnable.** A `complete` task re-runs via `complete → running` (re-validated
by the entry gate), opening a fresh **execution episode** — the `→running …
→complete` span in the append-only ledger; `satellites task executions <tsk-id>`
lists them oldest-first. The body holds the latest run's context; the ledger holds
them all.

**Driving a task** (the `running` executor's loop): open it through the entry
gate (`satellites-task-upsert-review`); do the work the body describes — if the
task carries a `skill:<name>` tag, run that work skill, otherwise inline — and
record a `## Report` (body or ledger); then close it through the exit gate
(`satellites-task-report-review`). The executor does the work and writes the
report; only a reviewer's accept moves status. The how-to lives in each gate's
rubric and the task's work skill — not in this workflow.

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
