---
name: urgent-workflow
type: skill
kind: workflow
tags: [kind:workflow]
applies_to: [urgent]
description: The lifecycle an `urgent` story follows — plan → in-progress → deploy → done, every edge reviewer-gated. A lean four-state fast path for work that must ship quickly WITHOUT skipping the gates. Invoke when implementing an `urgent` story; it IS the executor's process.
---

# Urgent workflow

This skill is the **process** for an `urgent` story. Urgent means *fast*, not
*ungated* — the whole point of satellites is that "done" is unreachable without
passing every door. This workflow keeps the loop short (four states, three
gates) so speed comes from removing ceremony, never from skipping review.

When asked to implement an urgent story, you read this skill and follow it: the
story is the goal, this workflow is the loop you run to reach it.

## The story is the contract; the plan is the loop

The story is the one self-describing artifact every gate reads. Record the
contract into it first, get the plan gate-approved, then execute.

1. Read the story (`document_get`) and its acceptance criteria — the goal.
2. **Record the contract into the story body** (`document_upsert` on the story
   id). Two sections:
   - **`## Workflow`** — copy this skill's states + transitions (the fenced
     ```yaml block at the foot of this skill) verbatim into the story. This
     pins the workflow the later gates follow; plan-review validates it once,
     and the later gates trust the story's copy.
   - **The plan** — Purpose / Approach / numbered Acceptance criteria.
3. Request the entry gate: `satellites story status_transition <story-id>`.
   **`urgent-plan-review`'s accept IS the approval of your plan** — the
   go-ahead to start (`plan → in-progress`). There is no separate sign-off.
4. Do the work the plan describes. Run the tests until they pass.
5. Request `satellites story status_transition <story-id>` again — **`urgent-deploy-review`**
   confirms the work is complete and the tests pass (`in-progress → deploy`).
6. Deploy the change (for this repo: `/commit-push` and let CI carry it
   through test → release → deploy; for another repo: that repo's ship step).
7. Request `satellites story status_transition <story-id>` a final time —
   **`urgent-done-review`** verifies the change actually shipped and closes it
   (`deploy → done`).

A rejected gate returns with notes; fix what it names and request again. Never
hand-patch status — only reviewers advance it.

## Transitions

Three reviewer gates, one per edge. Every edge is gated: a reviewer enacts the
move, so the loop never depends on an executor patching status.

- `backlog → in-progress` — `urgent-plan-review`: the story carries a sound,
  executable plan and a pinned `## Workflow`. The planning happens in `backlog`
  (the story's birth state); this gate's accept is the go-ahead to start.
- `in-progress → deploy` — `urgent-deploy-review`: the work is complete, the
  tests pass, and the acceptance criteria are met.
- `deploy → done` — `urgent-done-review`: the change was deployed/shipped and
  verified.

States and transitions live in the fenced ```yaml block below.

```yaml
states:
  - backlog
  - in-progress
  - deploy
  - done
transitions:
  - {from: backlog,     to: in-progress, reviewer_skill: "satellites-urgent-plan-review"}
  - {from: in-progress, to: deploy,      reviewer_skill: "satellites-urgent-deploy-review"}
  - {from: deploy,      to: done,        reviewer_skill: "satellites-urgent-done-review"}
```
