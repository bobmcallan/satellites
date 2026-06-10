---
name: satellites-fix-workflow
type: skill
kind: workflow
tags: [kind:workflow]
applies_to: [fix, refactor, bug, infrastructure]
description: The lifecycle a `fix`/`refactor`/`bug`/`infrastructure` story follows — backlog → in_progress → done, both edges reviewer-gated. Invoke when implementing such a story; it IS the executor's process.
---

# Fix workflow

The process for a `fix` / `refactor` / `bug` / `infrastructure` story. The story is the goal; this is the loop.

1. `document_get` the story; read its acceptance criteria.
2. Before starting, `document_upsert` two sections into the story body:
   - **`## Workflow`** — the fenced ```yaml block below, copied verbatim. Later gates parse the story's copy.
   - **The plan** — Purpose / Approach / numbered Acceptance criteria.
3. Request each gated transition: `satellites story status_transition <story-id> --skill <gate>`. The plan-review accept IS your go-ahead to start.
4. Do the work; commit at each checkpoint.
5. Request the next gate until `done`.

A rejected gate (or a missing/mismatched `## Workflow`) returns notes; fix and request again. Only reviewers advance status — never hand-patch it.

A client/server change is invisible to the gate until it is built, tested, committed, pushed through CI (test → release → deploy), and the client refreshed; the gate runs the local binary, so an unshipped change is not seen. Merging is not releasing — bump the client version so CI cuts a release, then refresh the client before driving the gate.

## Transitions

- `backlog → in_progress` — `satellites-story-plan-review` accepts the plan.
- `in_progress → done` — `satellites-story-done-review` verifies against the acceptance criteria.

```yaml
states:
  - backlog
  - in_progress
  - done
transitions:
  - {from: backlog,     to: in_progress, reviewer_skill: "satellites-story-plan-review"}
  - {from: in_progress, to: done,        reviewer_skill: "satellites-story-done-review"}
```

## Environment

Runs in the story's repo. Acts on it through `satellites` CLI verbs (`document_get`, `document_upsert`, `story status_transition`) and ordinary git commits at checkpoints. Status moves only through gated transitions.

```yaml
guardrails:
  always:
    - Upsert the verbatim `## Workflow` block and the plan before requesting the first gate.
    - Advance status only via `story status_transition --skill <gate>`; let the reviewer decide.
    - Commit at each checkpoint so the gate runs against committed work.
    - Address every rejection's notes before re-requesting the same gate.
  ask_first:
    - Force-pushing or rewriting a checkpoint commit's history.
    - Bumping the client version to cut a release as part of shipping a change.
  never:
    - Hand-patch a story's status field to skip or fake a gated transition.
    - Edit, delete, or reorder another story's `## Workflow` block.
```
