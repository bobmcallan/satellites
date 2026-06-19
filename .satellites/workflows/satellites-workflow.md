---
name: satellites-workflow
kind: workflow
tags: [kind:workflow]
applies_to: ["infrastructure"]
description: The reviewers-only lifecycle for this repo's infrastructure stories — backlog to in_progress to shipping to done. Two reviewers gate it (the only enforcement) — satellites-intent-plan-review opens the story, and satellites-commit-push-review judges that the executor's commit-push landed and advances shipping to done (a v2 pass/fail edge the client enacts; a fail returns to in_progress to repair and re-ship). The executor reaches the goal any way it can; only a reviewer's accept moves the status.
---

# Satellites workflow (reviewers-only)

The single enforcement primitive is the reviewer (see [[reviewer-only-model]]):
a `claude -p` judgment that enacts a transition and that the agent cannot bypass.
This workflow names only reviewers that exist, so every gated edge resolves.

1. **backlog → in_progress** — `satellites-intent-plan-review` opens the story
   (satellites-formatted, a clear story→done goal).
2. **in_progress** (executor) — do the work however you like, committing
   incrementally and locally (no `.version` bump, no push). At a natural
   checkpoint, advance the ungated `in_progress → shipping` checkpoint edge.
3. **shipping** (executor) — run the [[satellites-commit-push]] capability
   (final commit folding the incremental ones, `.version` bump when a binary path
   changed, push, watch the test → release → deploy CI chain). Then request the
   reviewer.
4. **shipping → done / in_progress** — `satellites-commit-push-review` judges that
   the push landed (HEAD pushed, clean tree, CI green, version bump when needed).
   The client enacts: **pass → done**, **fail → in_progress** to repair and
   re-ship. The reviewer is the ONLY status-updater; the capability never gates.

## Workflow

```yaml
states:
  - backlog
  - {name: in_progress, actor: executor}
  - {name: shipping, actor: executor}
  - {name: blocked, actor: operator}
  - done
transitions:
  - {from: backlog, to: in_progress, reviewer_skill: "satellites-intent-plan-review"}
  - {from: in_progress, to: shipping, trigger: checkpoint}
  - {from: shipping, to: done, reviewer_skill: "satellites-commit-push-review", on: pass}
  - {from: shipping, to: in_progress, reviewer_skill: "satellites-commit-push-review", on: fail, max_iterations: 3, on_exhausted: blocked}
```

## Environment

Drives this repo's infrastructure stories backlog → in_progress → shipping → done.
The entry is gated by the embedded intent spine; the commit-push exit is gated by
`satellites-commit-push-review` over a v2 pass/fail edge the client enacts.

```yaml
guardrails:
  always:
    - Drive the engaged story to done through the reviewer.
    - At shipping, run the satellites-commit-push capability (commit, .version bump, push, watch CI) BEFORE requesting satellites-commit-push-review.
    - Advance shipping → done only through the reviewer's accept — never set-status across it.
  ask_first: []
  never:
    - Move a story across a reviewer-gated edge with set-status.
    - Treat the satellites-commit-push capability as the gate — only the reviewer advances status.
    - Move a story across an edge this workflow does not declare.
```
