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
  acceptance criteria), carries a clear story→done goal, and records a resolvable
  governing workflow (a valid `workflow:` selector or a category default — no
  embedded ## Workflow required); and
- the **exit** (in_progress to done) is judged by `satellites-story-done-review`,
  which checks the story's numbered acceptance criteria are satisfied with
  evidence before it closes.

All three gates are EDITABLE `config/skills` reviewers resolved from the binary
embed — the resolver consults a repo's `.claude/skills/<name>` FIRST (operator
local-WINS) and falls back to the embed, so a repo may override ANY of them,
including the entry gate. There is no protected internal-gate home and no Go
special-case: the binary holds only the mechanism (run the named reviewer at the
edge, enforce reviewer-only), and an override flows through `satellites-skill-review`
like any other skill (epic:workflow-steps order-3). The cancel edges are gated by
`satellites-story-cancel-review`, which accepts a move to `cancelled` only on a
concrete `## Cancellation` rationale. Other gates (start / techdebt review, and
so on) remain an opt-in palette a richer repo-owned workflow composes.

## Workflow

- backlog to in_progress — open, gated by satellites-intent-plan-review (format + story→done goal).
- in_progress to done — close, gated by satellites-story-done-review (acceptance criteria satisfied).
- backlog/in_progress to cancelled — abandon, gated by satellites-story-cancel-review (concrete cancellation rationale).

```yaml
states:
  - backlog
  - {name: in_progress, actor: executor}
  - done
  - cancelled
transitions:
  - {from: backlog, to: in_progress, reviewer_skill: "satellites-intent-plan-review"}
  - {from: in_progress, to: done, reviewer_skill: "satellites-story-done-review"}
  - {from: backlog, to: cancelled, reviewer_skill: "satellites-story-cancel-review"}
  - {from: in_progress, to: cancelled, reviewer_skill: "satellites-story-cancel-review"}
```

## Environment

Drives a story document backlog to in_progress to done. The entry is gated by the
embed-first, repo-overridable intent spine reviewer; the exit is gated by the
done reviewer; the cancels are gated by the cancel reviewer. The goal-keeper Stop
hook holds the agent to a terminal state.

```yaml
guardrails:
  always:
    - Drive the engaged story to a terminal state (done) — the goal-keeper holds you to it.
    - Request the entry spine gate (satellites-intent-plan-review) to open a story; it checks the story is satellites-formatted with a story→done goal.
    - Request the exit gate (satellites-story-done-review) to close a story; it checks the acceptance criteria are satisfied with evidence.
    - To abandon a story, request the cancel gate (satellites-story-cancel-review) with a concrete ## Cancellation rationale — never set-status to cancelled.
  ask_first: []
  never:
    - Move a story across a gated edge with set-status — route it through the named gate.
    - Move a story across an edge its governing workflow does not declare.
```
