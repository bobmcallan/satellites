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
gate (`satellites-task-upsert-review`); if the body declares document inputs (an
optional `## Inputs` block — see below) resolve them first with
`satellites task inputs <tsk-id>` and read each; do the work the body describes —
if the task carries a `skill:<name>` tag, run that work skill, otherwise inline —
emit the declared OUTPUT (a first-class document — see below) with
`satellites task output <tsk-id>` and record a `## Report` (body or ledger); then
close it through the exit gate (`satellites-task-report-review`). The executor does
the work and writes the report; only a reviewer's accept moves status. The how-to
lives in each gate's rubric and the task's work skill — not in this workflow.

**`## Inputs` — declared document inputs (optional).** A task MAY assess its
project's documents (wiki, notes, prior artifacts) as declared inputs. Add an
`## Inputs` section with a fenced yaml block naming a KV filter and/or explicit
ids:

```text
tags: [phase:discovery]   # KV filter (phase:/type: …), AND containment
ids:  [doc_abc, doc_def]  # explicit document ids (optional)
project: proj_xxxx        # optional: a read-only MOUNTED project to read from
```

`satellites task inputs <tsk-id>` resolves the set pinned to the task's own
project by default, so inputs never cross a project boundary unless explicitly
asked. Inputs are the source project's `type:document` rows; the executor reads
each body via `document_get` (or `task inputs --read`). Like a `skill:<name>`
reference, declared inputs are executor CONTEXT, not a gated dependency.

**Cross-repo inputs come only through a read-only mount.** When `## Inputs` names a
`project:`, that project MUST be mounted read-only into the task's workspace
(`workspace_mount_list`); the resolver pins to it and the verb layer authorises the
read via the mount grant. A mount grants READ only — a task can never write to a
mounted project — and the executor still cannot reach the mounted repo's files
directly (the `claude -p` worktree boundary holds); cross-repo content arrives only
as mounted DOCUMENTS, never raw filesystem.

**Output — a first-class document.** A task's OUTPUT is more than a path in the
body: a successful run emits a typed project document with
`satellites task output <tsk-id> --name <name> [--kind document|diagram]
[--phase <phase>] (--body <md> | --body-file <path>)`. The output is attached to
the task's own project and linked back to the run two ways — a `task:<tsk-id>` KV
tag on the document (so `document_list {project_id, tags:[task:<tsk-id>]}`
enumerates a task's outputs) and a `log:task_output` ledger row in the run's
episode. The exit gate may treat that output document as the deliverable that
satisfies the task's VERIFICATION. Outputs land in the task's own project
(cross-repo output is out of scope).

**Authoring a task** — the task body IS the work definition (see
[[work-artifact-selection]]): state the ACTION, the OUTPUT, and how success is
VERIFIED, directly in the body. The entry reviewer judges that structure; the
agent does the work during `running`; the exit reviewer judges the output against
the declared verification. A task MAY name a skill for the agent to use, but that
is the agent's Claude capability — a warning context, not a substrate dependency
satellites resolves.

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
