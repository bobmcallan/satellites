---
name: satellites-baseline-workflow
scope: system
kind: workflow
tags: [kind:workflow]
applies_to: ["*"]
description: The order-zero baseline lifecycle — backlog to in_progress to done. The entry is gated by the spine plan-gate (satellites-intent-plan-review) so a story is judged satellites-formatted with a clear story→done goal before work begins; the exit is gated by satellites-story-done-review so a story closes on satisfied acceptance criteria, not "looks finished". Both gates are system substrate resolved from the binary embed. Cancel edges are ungated. Other gates stay an opt-in palette a richer repo-owned workflow composes.
---

# Baseline workflow (order-zero)

The default governing workflow every adopter gets: a story moves backlog to
in_progress to done. Two SYSTEM gates bracket it — both authored in `config/`,
embedded in the client binary, resolved from embed (not a Go const, not server
rows):

- the **entry** (backlog to in_progress) is judged by `satellites-intent-plan-review`,
  which checks the story is satellites-formatted (Purpose / Approach / numbered
  acceptance criteria / an embedded ## Workflow) and carries a clear story→done
  goal; and
- the **exit** (in_progress to done) is judged by `satellites-story-done-review`,
  which checks the story's numbered acceptance criteria are satisfied with
  evidence before it closes.

The entry gate is a satellites-INTERNAL gate (injected from the binary, never
materialised to .claude/skills, so it cannot be edited); the done gate is an
editable `config/skills` reviewer resolved from the binary embed — a repo may
override it via `.claude/skills`. The cancel edges are ungated — advance them
with `satellites story set-status <story-id> cancelled`. Other gates (start /
techdebt review, and so on) remain an opt-in palette a richer repo-owned
workflow composes.

## Workflow

- backlog to in_progress — open, gated by satellites-intent-plan-review (format + story→done goal).
- in_progress to done — close, gated by satellites-story-done-review (acceptance criteria satisfied).
- backlog/in_progress to cancelled — abandon (ungated).

```yaml
states:
  - backlog
  - {name: in_progress, actor: executor}
  - done
  - cancelled
transitions:
  - {from: backlog, to: in_progress, reviewer_skill: "satellites-intent-plan-review"}
  - {from: in_progress, to: done, reviewer_skill: "satellites-story-done-review"}
  - {from: backlog, to: cancelled}
  - {from: in_progress, to: cancelled}
```

## Environment

Drives a story document backlog to in_progress to done. The entry is gated by the
internal intent spine; the exit is gated by the done reviewer; the cancels are
ungated status moves. The goal-keeper Stop hook holds the agent to a terminal
state.

```yaml
guardrails:
  always:
    - Drive the engaged story to a terminal state (done) — the goal-keeper holds you to it.
    - Request the entry spine gate (satellites-intent-plan-review) to open a story; it checks the story is satellites-formatted with a story→done goal.
    - Request the exit gate (satellites-story-done-review) to close a story; it checks the acceptance criteria are satisfied with evidence.
  ask_first: []
  never:
    - Move a story across a gated edge with set-status — route it through the named gate.
    - Move a story across an edge its governing workflow does not declare.
```
