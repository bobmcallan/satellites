---
name: feature-workflow
applies_to: [feature]
---

# Feature workflow

The default lifecycle for `feature` stories. States use the canonical
story taxonomy from `docs/story-schema.md` (`backlog · ready ·
in_progress · review · done`) so a status the substrate persists is
always a state the workflow knows — no underscore-vs-hyphen drift.

Three reviewer gates, one per transition. Every edge is gated: a
reviewer enacts the move, so the loop never depends on an executor
patching status (which the role gate forbids — only reviewers advance
status).

- `backlog → ready` — `satellites-story-plan-review` must accept the plan
  before the story is ready to pick up. The executor plans while the story
  sits in `backlog`.
- `ready → in_progress` — `satellites-story-start-review` confirms the story
  is ready to start (plan accepted, no open blockers) and enacts the move.
  This replaces the old ungated self-transition, which had no
  executor-drivable mover and dead-ended feature stories at `ready`
  (sty_3934ad71).
- `in_progress → done` — `satellites-story-done-review` verifies the change
  against the acceptance criteria.

States and transitions live in the fenced ```yaml block below. Free
text around it is for human readers — the parser only reads what's
inside the block.

```yaml
states:
  - backlog
  - ready
  - in_progress
  - done
transitions:
  - {from: backlog,     to: ready,       reviewer_skill: "satellites-story-plan-review"}
  - {from: ready,       to: in_progress, reviewer_skill: "satellites-story-start-review"}
  - {from: in_progress, to: done,        reviewer_skill: "satellites-story-done-review"}
```
