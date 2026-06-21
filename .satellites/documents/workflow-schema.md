---
name: workflow-schema
type: document
scope: project
tags: [area:workflow, area:substrate, kind:contract]
description: The pinned vocabulary (skill · reviewer · gate · step · workflow · work-item) and the terse-markdown workflow shape — source of truth for the epic:workflow-steps migration. Links [[constitution]] and [[reviewer-only-model]].
---

# Workflow schema — vocabulary + terse markdown

The pinned vocabulary and the minimal markdown shape for workflows, locked
**before** any migration so user and agent share one non-overlapping set of
terms. This is the source of truth the rest of `epic:workflow-steps` builds on.

Holds to [[constitution]] (mechanism in the binary, behaviour in substrate) and
[[reviewer-only-model]] (only a reviewer's accept moves status).

## Vocabulary (no overlap)

Six terms, each with one meaning and one owner. Nothing here overlaps anything
else — if two terms seem to describe the same thing, one of them is wrong.

- **skill** — a unit of *work* the executor performs (the "do"; markdown how-to).
  Examples: `commit-push`, `secrets-scan`. A skill never judges or moves status.
- **reviewer** — a `kind:reviewer` skill that *judges and enacts* (the "check";
  markdown rubric). It returns `{decision, notes}` and, on accept, writes the
  `status_transition` ledger row itself. The reviewer is the ONLY thing that
  moves status.
- **gate** — the *execution* of a reviewer at a transition: one `claude -p` run
  that enacts. A gate is NOT a separate artifact — it is a reviewer running on an
  edge. ("No gate as code": the reviewer is substrate; the binary only runs it.)
- **step** — `{work_skill, reviewer}`: a do→check pair. The reusable atom.
  `commit-push` (the `commit-push` work skill + `satellites-commit-push-review`)
  is the canonical example. `work_skill` is optional; `reviewer` is what gates
  the edge.
- **workflow** — an ordered set of steps + states. A reusable template in
  **markdown** (terse — NOT YAML-as-config; the state machine is a small fenced
  block, the rest is the step list).
- **work-item** — the subject a workflow runs over:
  - **story** = run **once** → `done` (resolves a categorised workflow), and
  - **task** = run **repeatably** (execution episodes; its own/basic workflow).

### The de-duplication

A standalone **task is a one-step repeatable workflow**. A **story-workflow step
IS a task-shaped atom**. The same `{work_skill, reviewer}` step is the unit at
every scale — that is the whole point of pinning these terms: one atom, no
duplicated vocabulary.

## Terse markdown shape

A workflow is a markdown document (frontmatter + prose + one fenced state
machine). It carries its states and transitions in a `## Workflow` section and
needs **no verbose `## Environment` narrative** — prose belongs in the reviewer
rubric (judgment) and the work skill (how-to), never in workflow narrative.

### Frontmatter

```yaml
name: <kebab-name>
type: document            # or a kind:workflow skill
kind: workflow
applies_to: ["<category>", ...]   # the work-item categories this workflow governs ("*" = wildcard)
description: <one line>
```

### `## Workflow` — states + transitions

A short human-readable list of the edges, followed by ONE fenced ```yaml block:

```yaml
states:
  - backlog
  - {name: in_progress, actor: executor}
  - done
transitions:
  - {from: backlog, to: in_progress, reviewer_skill: "satellites-intent-plan-review"}
  - {from: in_progress, to: done, work_skill: "commit-push", reviewer_skill: "satellites-commit-push-review"}
```

Transition fields:

| Field            | Required | Meaning                                                            |
| ---------------- | -------- | ------------------------------------------------------------------ |
| `from`           | yes      | Source state — must appear in `states`.                            |
| `to`             | yes      | Destination state — must appear in `states`.                       |
| `reviewer_skill` | no       | The reviewer that gates this edge; empty = ungated (trigger edge). |
| `work_skill`     | no       | The work skill of this edge's step (the "do" half). Display/route. |

A transition that names both `work_skill` and `reviewer_skill` IS a step. An
ungated edge (no `reviewer_skill`) is a `trigger: checkpoint` — the executor's
deliberate move, never a silent side-effect of a gate.

## What this schema is NOT

- It is **not** an engine change. Parsing `work_skill` first-class lands in
  epic-order:2; this doc only PINS the shape.
- It is **not** YAML-as-process. The fenced block is a small state machine; the
  workflow stays markdown so it reads as prose, not config.
- It does **not** re-home process into the binary. The binary runs the named
  reviewer; the workflow, steps, and rubrics are all substrate ([[constitution]]).
