---
name: satellites-baseline-workflow
kind: workflow
tags: [kind:workflow]
applies_to: ["*"]
description: The order-zero baseline lifecycle — backlog to in_progress to done. The entry is gated by the spine plan-gate (satellites-intent-plan-review, injected from the binary) so a story is judged satellites-formatted with a clear story→done goal before work begins; the exit is ungated. Other gates stay an opt-in palette a richer repo-owned workflow composes.
---

# Baseline workflow (order-zero)

The minimal governing workflow: a story moves backlog to in_progress to done.
The ONLY gate is the spine every repo gets — the entry transition (backlog to
in_progress) is judged by satellites-intent-plan-review, which checks the story
is satellites-formatted (Purpose / Approach / numbered acceptance criteria / an
embedded ## Workflow) and carries a clear story→done goal. The spine gates are
satellites-INTERNAL: injected from the binary, never materialised to
.claude/skills, so they cannot be edited.

The exit (in_progress to done) and the cancel edges are ungated — advance them
with: satellites story set-status <story-id> <status>. The goal-keeper Stop hook
holds the agent to a terminal state. Other gates (start / techdebt / done review,
and so on) remain an opt-in palette a richer repo-owned workflow composes; this
baseline names only the intent spine.

## Workflow

- backlog to in_progress — open, gated by satellites-intent-plan-review (format + story→done goal).
- in_progress to done — close (ungated).
- backlog/in_progress to cancelled — abandon (ungated).

```yaml
states:
  - backlog
  - {name: in_progress, actor: executor}
  - done
  - cancelled
transitions:
  - {from: backlog, to: in_progress, reviewer_skill: "satellites-intent-plan-review"}
  - {from: in_progress, to: done}
  - {from: backlog, to: cancelled}
  - {from: in_progress, to: cancelled}
```

## Environment

Drives a story document backlog to in_progress to done. The entry is gated by the
internal intent-gate; the exit and cancels are ungated status moves. The
goal-keeper Stop hook holds the agent to a terminal state.

```yaml
guardrails:
  always:
    - Drive the engaged story to a terminal state (done) — the goal-keeper holds you to it.
    - Request the entry spine gate (satellites-intent-plan-review) to open a story; it checks the story is satellites-formatted with a story→done goal.
  ask_first: []
  never:
    - Move a story across a gated edge with set-status — route it through the named gate.
    - Move a story across an edge its governing workflow does not declare.
```
