---
name: feature-workflow
applies_to: [feature]
---

# Feature workflow

The default lifecycle for `feature` stories. States use the canonical
story taxonomy from `docs/story-schema.md` (`backlog · ready ·
in_progress · done`) so a status the substrate persists is always a
state the workflow knows — no underscore-vs-hyphen drift.

Two reviewer gates, on the two transitions that promise the operator
something is true: a plan is ready, and the work is done.

- `backlog → ready` — `plan-review` must accept the plan before the
  story is ready to pick up.
- `ready → in_progress` — agent starts coding; no reviewer required.
- `in_progress → done` — `done-review` skill verifies the change.

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
  - {from: backlog,     to: ready,       reviewer_skill: "plan-review"}
  - {from: ready,       to: in_progress, reviewer_skill: ""}
  - {from: in_progress, to: done,        reviewer_skill: "done-review"}
```
