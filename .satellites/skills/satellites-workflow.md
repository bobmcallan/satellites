---
name: satellites-workflow
type: skill
kind: workflow
tags: [kind:workflow]
applies_to: ["*"]
description: The lifecycle EVERY satellites story follows (any category) — backlog → ready → in_progress → done, every edge reviewer-gated, with the commit-time gates named in this definition. Invoke when implementing a story; it IS the executor's process.
---

# Satellites workflow

The single executable workflow for this repo's stories, whatever their
category. The story is the goal; this is the loop. (Anchors — parent stories
that group children and carry no executable work — follow
`satellites-parent-workflow` instead.)

1. `document_get` the story; read its acceptance criteria.
2. Before starting, `document_upsert` two sections into the story body:
   - **`## Workflow`** — the fenced ```yaml block below, copied verbatim. Later gates parse the story's copy.
   - **The plan** — Purpose / Approach / numbered Acceptance criteria.
3. Request each gated transition: `satellites story status_transition <story-id> --skill <gate>`. The plan-review accept IS your go-ahead to start.
4. Do the work; run the **checkpoint** (below) at every natural checkpoint.
5. Request the next gate until `done`.

A rejected gate (or a missing/mismatched `## Workflow`) returns notes; fix and request again. Only reviewers advance status — never hand-patch it.

A client/server change is invisible to the gate until it is built, tested, committed, pushed through CI, and the client refreshed; the gate runs the local binary, so an unshipped change is not seen.

## Transitions

- `backlog → ready` — `satellites-story-plan-review` accepts the plan.
- `ready → in_progress` — `satellites-story-start-review` confirms ready to start.
- `in_progress → done` — `satellites-story-done-review` verifies against the acceptance criteria.
- `backlog/ready/in_progress → cancelled` — `satellites-story-cancel-review`.

## Checkpoint gates

This definition names every fail-closed check that runs while driving a story —
nothing gate-like runs outside it. At every natural checkpoint (end of phase,
meaningful change, before requesting review) run the [[satellites-commit-push]]
capability, which executes these atomic gates and honours their verdicts (each
gate owns its decision rule):

- [[satellites-technical-debt-review]] — always, pre-commit.
- [[satellites-doc-drift-review]] — when the change touches the CLI.

```yaml
states:
  - backlog
  - ready
  - in_progress
  - done
  - cancelled
transitions:
  - {from: backlog,     to: ready,       reviewer_skill: "satellites-story-plan-review"}
  - {from: ready,       to: in_progress, reviewer_skill: "satellites-story-start-review"}
  - {from: in_progress, to: done,        reviewer_skill: "satellites-story-done-review"}
  - {from: backlog,     to: cancelled,   reviewer_skill: "satellites-story-cancel-review"}
  - {from: ready,       to: cancelled,   reviewer_skill: "satellites-story-cancel-review"}
  - {from: in_progress, to: cancelled,   reviewer_skill: "satellites-story-cancel-review"}
```

## Environment

Drives story documents and the repo working tree: upserts the story body,
requests gated transitions, and triggers checkpoint commits.

```yaml
guardrails:
  always:
    - Copy the Workflow yaml block into the story verbatim before requesting a gate.
    - Route every status change through the transition's reviewer skill.
    - Run the checkpoint capability (and its gates) before requesting review.
  ask_first:
    - Cancelling a story (the rationale is the operator's call unless already given).
  never:
    - Hand-patch story status or bypass a gate verdict.
    - Run a fail-closed check this definition does not name.
```
