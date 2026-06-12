---
name: satellites-workflow
type: skill
kind: workflow
tags: [kind:workflow]
applies_to: ["*"]
description: The lifecycle EVERY satellites story follows (any category) — backlog → ready → in_progress → techdebt-review → done-review → done, reviews as visible states with actors, fail loops bounded in code (×3, exhaustion → blocked). Invoke when implementing a story; it IS the executor's process.
---
<!-- satellites-sync:begin {"document_id":"doc_67945dca","version":5,"hash":"ab6e0e86c8cf06ed757dc3c641bbdc08ca707fb93920e75b65f20456148d2939"} satellites-sync:end -->

# Satellites workflow

The single executable workflow for this repo's stories, whatever their
category. The story is the goal; this is the loop. (Anchors — parent stories
that group children and carry no executable work — follow
`satellites-parent-workflow` instead.) Reviews are STATES with actors: the
status itself answers "whose turn is it, and was the gate run?".

1. `document_get` the story; read its acceptance criteria.
2. Before starting, `document_upsert` two sections into the story body:
   - **`## Workflow`** — the fenced ```yaml block below, copied verbatim. Later gates parse the story's copy.
   - **The plan** — Purpose / Approach / numbered Acceptance criteria.
3. Request the entry gates: `satellites story status_transition <story-id> --skill <gate>`
   for plan-review (backlog → ready) and start-review (ready → in_progress).
4. Do the work. At every natural checkpoint request the traverse FIRST:
   `satellites story status_transition <story-id> --skill
   satellites-technical-debt-review` — the client enacts the checkpoint edge
   into `techdebt-review` and runs that state's command itself against the
   LOCAL working tree (exit code = pass/fail; no judgment anywhere): a fail
   returns the story to `in_progress` with nothing shipped (the 3rd fail
   escalates to `blocked` by the client's own enactment); a pass lands
   `done-review` and releases the ship.
5. On pass, run the **checkpoint capability** (below) — commit, push, watch
   CI, record evidence — so the verified tree becomes the pushed commit the
   reviewer judges. Nothing ships from a tree whose traverse failed.
6. From `done-review`, request `satellites story status_transition <story-id>
   --skill satellites-story-done-review`. The gate JUDGES ONLY on these edges —
   the client enacts pass → `done` / fail → back to `in_progress` (×3, then
   `blocked`).

A rejected gate returns notes; fix and request again — each reject is a real
transition back to `in_progress`, visible on the ledger. Only reviewers and the
client's deterministic enactment advance status — never hand-patch it. A story
in `blocked` is the operator's: not your state → stop.

A client/server change is invisible to the gate until it is built, tested,
committed, pushed through CI, and the client refreshed; the gate runs the local
binary, so an unshipped change is not seen.

## States and actors

- `in_progress` (executor) — the work happens here, and every fail edge lands back here.
- `techdebt-review` (satellites) — advanced by the client running `satellites techdebt review`; exit code decides, no agent discretion.
- `done-review` (reviewer) — `satellites-story-done-review` judges; the client enacts its decision.
- `blocked` (operator) — fail-loop exhaustion lands here; only the operator moves a story out.

## Checkpoint gates

This definition names every fail-closed check that runs while driving a story —
nothing gate-like runs outside it. The checkpoint order at every natural
checkpoint (end of phase, meaningful change, before requesting review) is
**verdict, then ship**:

- [[satellites-technical-debt-review]] — the `techdebt-review` STATE's command,
  run via the status_transition traverse in step 4 against the local working
  tree BEFORE anything ships. Its verdict lands as a ledger row on the story,
  and a fail leaves the remote untouched. The gate skill owns the decision rule.

A traverse pass releases the [[satellites-commit-push]] capability (step 5),
which executes the remaining atomic gates pre-commit and honours their verdicts
(each gate owns its decision rule):

- [[satellites-doc-drift-review]] — when the change touches the CLI.
- [[satellites-global-button-style-review]] — when the change touches the portal UI.
- [[satellites-workflow-drift-review]] — when the change touches process configuration (skills, principles, workflows).

```yaml
states:
  - backlog
  - ready
  - {name: in_progress,     actor: executor}
  - {name: techdebt-review, actor: satellites, command: "satellites techdebt review"}
  - {name: done-review,     actor: reviewer}
  - {name: blocked,         actor: operator}
  - done
  - cancelled
transitions:
  - {from: backlog,         to: ready,           reviewer_skill: "satellites-story-plan-review"}
  - {from: ready,           to: in_progress,     reviewer_skill: "satellites-story-start-review"}
  - {from: in_progress,     to: techdebt-review, trigger: checkpoint}
  - {from: techdebt-review, on: pass, to: done-review}
  - {from: techdebt-review, on: fail, to: in_progress, max_iterations: 3, on_exhausted: blocked}
  - {from: done-review,     on: pass, to: done, reviewer_skill: "satellites-story-done-review"}
  - {from: done-review,     on: fail, to: in_progress, max_iterations: 3, on_exhausted: blocked, reviewer_skill: "satellites-story-done-review"}
  - {from: backlog,         to: cancelled,       reviewer_skill: "satellites-story-cancel-review"}
  - {from: ready,           to: cancelled,       reviewer_skill: "satellites-story-cancel-review"}
  - {from: in_progress,     to: cancelled,       reviewer_skill: "satellites-story-cancel-review"}
```

## Environment

Drives story documents and the repo working tree: upserts the story body,
requests gated transitions, and triggers checkpoint commits.

```yaml
guardrails:
  always:
    - Copy the Workflow yaml block into the story verbatim before requesting a gate.
    - Route every status change through the transition's reviewer skill or the client's deterministic enactment.
    - Run the techdebt-review traverse to a pass before the checkpoint capability ships anything.
  ask_first:
    - Cancelling a story (the rationale is the operator's call unless already given).
  never:
    - Hand-patch story status or bypass a gate verdict.
    - Act in a state whose actor is not yours — blocked is the operator's.
    - Run a fail-closed check this definition does not name.
```
