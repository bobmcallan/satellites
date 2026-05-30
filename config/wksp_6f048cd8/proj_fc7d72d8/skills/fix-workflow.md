---
name: fix-workflow
applies_to: [fix]
---

# Fix workflow

The lifecycle for `fix` stories — smaller, scoped changes that do not
warrant a plan gate. States use the canonical story taxonomy from
`docs/story-schema.md` (`backlog · in_progress · done`), so a status
the substrate persists is always a state the workflow knows.

Two reviewer gates:

- `backlog → in_progress` — `plan-review` checks the story has a sound,
  executable plan before an executor starts the fix. The reviewer
  enacts the transition on accept.
- `in_progress → done` — `done-review` verifies the fix against the
  acceptance criteria.

States and transitions live in the fenced ```yaml block below. Free
text around it is for human readers — the parser only reads what's
inside the block.

```yaml
states:
  - backlog
  - in_progress
  - done
transitions:
  - {from: backlog,     to: in_progress, reviewer_skill: "plan-review"}
  - {from: in_progress, to: done,        reviewer_skill: "done-review"}
```
