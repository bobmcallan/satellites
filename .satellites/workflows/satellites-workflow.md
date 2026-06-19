---
name: satellites-workflow
kind: workflow
tags: [kind:workflow]
applies_to: ["infrastructure"]
description: The satellites repo's own governing workflow for infrastructure stories — the order-zero baseline (backlog → in_progress gated by satellites-intent-plan-review; the cancel edges) PLUS a governed commit-push step. After in_progress the executor enters `shipping` and runs the satellites-commit-push capability (commit, .version bump, push, watch CI); the `shipping → done` transition is then judged by the satellites-commit-push-review REVIEWER (a v2 pass/fail edge the client enacts). Only the reviewer advances status; the capability is not the gate. Scoped to this repo via the repo-owned file — other repos keep the binary's embedded baseline.
---

# satellites repo workflow (baseline + governed commit-push)

This repo's infrastructure stories replicate the order-zero baseline and add a
GOVERNED commit-push step, so a story reaches `done` through a reviewer, not
ad-hoc manual git. It supersedes the embedded baseline for THIS repo only (an
exact `applies_to: ["infrastructure"]` match wins over the baseline `*` wildcard);
other repos are unaffected.

The commit-push step is the two-execution model the reviewers-only design names:

1. the EXECUTOR enters `shipping` and runs the `satellites-commit-push` capability
   (delivered by its `when: status==shipping`) — commit, bump `.version`, push,
   watch the test → release → deploy CI chain;
2. the `satellites-commit-push-review` REVIEWER judges that the push landed and
   the client enacts the `shipping → done` pass edge. A reject returns to
   `in_progress` (bounded), and the exhausted loop lands in `blocked` for the
   operator. The reviewer is the ONLY status-updater; the capability never gates.

## Workflow

- backlog → in_progress — open, gated by `satellites-intent-plan-review`.
- in_progress → shipping — the executor's checkpoint once the work is done (it then runs the commit-push capability at `shipping`).
- shipping → done — judged by `satellites-commit-push-review` (on pass).
- shipping → in_progress — on a reject (the push did not land / CI red), bounded; the Nth reject escalates to `blocked`.
- backlog/in_progress/shipping → cancelled — abandon (ungated).

```yaml
states:
  - backlog
  - {name: in_progress, actor: executor}
  - {name: shipping, actor: executor}
  - {name: blocked, actor: operator}
  - done
  - cancelled
transitions:
  - {from: backlog, to: in_progress, reviewer_skill: "satellites-intent-plan-review"}
  - {from: in_progress, to: shipping, trigger: checkpoint}
  - {from: shipping, to: done, reviewer_skill: "satellites-commit-push-review", on: pass}
  - {from: shipping, to: in_progress, reviewer_skill: "satellites-commit-push-review", on: fail, max_iterations: 3, on_exhausted: blocked}
  - {from: backlog, to: cancelled}
  - {from: in_progress, to: cancelled}
  - {from: shipping, to: cancelled}
```

## Environment

Drives this repo's infrastructure stories backlog → in_progress → shipping → done.
The entry is gated by the internal intent spine; the commit-push exit is gated by
the `satellites-commit-push-review` reviewer over a v2 pass/fail edge the client
enacts. The goal-keeper Stop hook holds the agent to a terminal state.

```yaml
guardrails:
  always:
    - Drive the engaged story to a terminal state (done) through the reviewer — the goal-keeper holds you to it.
    - At `shipping`, run the satellites-commit-push capability (commit, .version bump, push, watch CI) BEFORE requesting the commit-push-review reviewer.
    - Advance `shipping → done` only through the satellites-commit-push-review reviewer's accept — never set-status across it.
  ask_first: []
  never:
    - Move a story across a reviewer-gated edge with set-status — route it through the named reviewer.
    - Treat the satellites-commit-push capability as the gate — only the reviewer advances status.
    - Move a story across an edge its governing workflow does not declare.
```
