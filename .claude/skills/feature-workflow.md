---
name: feature-workflow
applies_to: [feature, fix]
---

# Feature workflow

The default story lifecycle for `feature` and `fix` stories. Five
states with reviewer gates on the two transitions that promise the
operator something is true: a plan is ready, work is done.

- `backlog → planning` — agent starts thinking; no reviewer required.
- `planning → planned` — `plan-review` skill must accept the plan.
- `planned → in-progress` — agent starts coding; no reviewer required.
- `in-progress → completed` — `done-review` skill verifies the change.

States and transitions live in the fenced ```yaml block below. Free
text around it is for human readers — the parser only reads what's
inside the block.

```yaml
states:
  - backlog
  - planning
  - planned
  - in-progress
  - completed
transitions:
  - {from: backlog,     to: planning,    reviewer_skill: ""}
  - {from: planning,    to: planned,     reviewer_skill: "plan-review"}
  - {from: planned,     to: in-progress, reviewer_skill: ""}
  - {from: in-progress, to: completed,   reviewer_skill: "done-review"}
```
