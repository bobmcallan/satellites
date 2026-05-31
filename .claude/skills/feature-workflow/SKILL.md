---
name: feature-workflow
description: The lifecycle a `feature` story follows — backlog → ready → in_progress → done, every edge reviewer-gated. Invoke when implementing a feature story; it IS the executor's process.
applies_to: [feature]
version: 1
---

# Feature workflow

This skill is the **process** for a `feature` story. When asked to implement
one, you read it and follow it: the story is the goal, this workflow is the
loop you run to reach it.

## The story is the goal; the plan is the loop

1. Read the story (`document_get`) and its acceptance criteria — the goal.
2. Write the **plan** into the story body (Purpose / Approach / numbered
   Acceptance criteria). The plan is the loop you will run.
3. Request the entry gate: `.satellites/satellites story review <story-id>`.
   **`satellites-story-plan-review`'s accept IS the approval of your plan** —
   the go-ahead to start. There is no separate operator sign-off.
4. Do the work the plan describes; commit at each checkpoint.
5. Request review at each subsequent gated transition until `done`.

Plan first, gate-approve the plan, then execute. A rejected plan returns with
notes; fix and request again. Never hand-patch status — only reviewers
advance it.

### Satellites-client changes need the full loop

A change to the satellites client/server is invisible to the gate until it is
built, locally tested, `/commit-push`ed, carried through CI
(test → release → deploy), and `.satellites/satellites` is refreshed to the
new release. The gate runs the LOCAL binary, so a client change you have not
shipped + refreshed will not be seen. Build → test → commit-push → watch CI →
refresh → then drive the gate.

## Transitions

Three reviewer gates, one per transition. Every edge is gated: a reviewer
enacts the move, so the loop never depends on an executor patching status
(the role gate forbids that — only reviewers advance status).

- `backlog → ready` — `satellites-story-plan-review` must accept the plan
  before the story is ready to pick up. The executor plans while the story
  sits in `backlog`.
- `ready → in_progress` — `satellites-story-start-review` confirms the story
  is ready to start (plan accepted, no open blockers) and enacts the move.
- `in_progress → done` — `satellites-story-done-review` verifies the change
  against the acceptance criteria.

States and transitions live in the fenced ```yaml block below. Free text
around it is for human readers — the parser only reads what's inside the
block.

```yaml
states:
  - backlog
  - ready
  - in_progress
  - done
transitions:
  - {from: backlog,     to: ready,       reviewer_skill: "satellites-story-plan-review"}
  - {from: ready,       to: in_progress, reviewer_skill: "satellites-story-start-review"}
  - {from: in_progress, to: done,        reviewer_skill: "satellites-story-done-review"}
```
