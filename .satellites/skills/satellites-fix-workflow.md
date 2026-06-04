---
name: satellites-fix-workflow
type: skill
kind: workflow
tags: [kind:workflow]
applies_to: [fix, refactor, bug, infrastructure]
description: The lifecycle a `fix`/`refactor`/`bug`/`infrastructure` story follows — backlog → in_progress → done, both edges reviewer-gated. Invoke when implementing such a story; it IS the executor's process.
---

# Fix workflow

This skill is the **process** for a `fix` (and `refactor` / `bug` /
`infrastructure`) story. When asked to implement one, you read it and follow
it: the story is the goal, this workflow is the loop you run to reach it.

## The story is the contract; the plan is the loop

The story is the one self-describing artifact the reviewer reads. At planning
you record BOTH the matched workflow and the plan into the story body, so every
later gate judges against the story alone.

1. Read the story (`document_get`) and its acceptance criteria — the goal.
2. **Record the contract into the story body** (`document_upsert` on the story
   id). Two sections:
   - **`## Workflow`** — copy this skill's states + transitions (the fenced
     ```yaml block at the foot of this skill) verbatim into the story. This
     pins the workflow the later gates follow; plan-review validates it against
     this skill once, and done-review then follows what the story records
     rather than re-resolving.
   - **The plan** — Purpose / Approach / numbered Acceptance criteria. The plan
     is the loop you will run.
3. Request the entry gate: `.satellites/satellites story status_transition <story-id>`.
   **`satellites-story-plan-review`'s accept IS the approval of your plan** —
   the go-ahead to start, advancing the story `backlog → in_progress`. There
   is no separate operator sign-off.
4. Do the work the plan describes; commit at each checkpoint.
5. Request the completion gate; `satellites-story-done-review`'s accept moves
   the story to `done`.

Record the contract first, gate-approve it, then execute. A rejected plan (or a
missing/mismatched `## Workflow`) returns with notes; fix and request again.
Never hand-patch status — only reviewers advance it.

### Satellites-client changes need the full loop

A change to the satellites client/server is invisible to the gate until it is
built, locally tested, `/commit-push`ed, carried through CI
(test → release → deploy), and `.satellites/satellites` is refreshed to the
new release. The gate runs the LOCAL binary, so a client change you have not
shipped + refreshed will not be seen. Build → test → commit-push → watch CI →
refresh → then drive the gate.

## Transitions

Two reviewer gates, one per transition — both reviewer-enacted, so the loop
never depends on an executor patching status (the role gate forbids that —
only reviewers advance status).

- `backlog → in_progress` — `satellites-story-plan-review` checks the story
  has a sound, executable plan before an executor starts; it enacts the
  transition on accept.
- `in_progress → done` — `satellites-story-done-review` verifies the change
  against the acceptance criteria.

States and transitions live in the fenced ```yaml block below. Free text
around it is for human readers — the parser only reads what's inside the
block.

```yaml
states:
  - backlog
  - in_progress
  - done
transitions:
  - {from: backlog,     to: in_progress, reviewer_skill: "satellites-story-plan-review"}
  - {from: in_progress, to: done,        reviewer_skill: "satellites-story-done-review"}
```
