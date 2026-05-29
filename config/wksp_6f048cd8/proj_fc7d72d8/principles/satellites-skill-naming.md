---
name: satellites-skill-naming
type: document
tags: [principles:project]
---

# Satellites skill naming

## Every satellites-managed skill is prefixed `satellites-`

The prefix marks a skill as owned by the substrate, not the operator.
`satellites-init` uses it to reconcile the local `.claude/skills/` tree:
it syncs and, crucially, **removes** `satellites-*` skills that no
longer exist in the substrate so renames and deletions propagate.
Skills without the prefix are operator-authored and are never touched.

A skill that ships without the prefix cannot be cleaned up on rename —
the stale copy lingers and the agent runs against it.

## Reviewer gates: `satellites-<object>-<stage>-review`

- **object** — what is gated: `story`, `doc`, …
- **stage** — the transition guarded: `start`, `plan`, `done`, …

The story-lifecycle gates are therefore:

- `satellites-story-start-review` — entry to work (`backlog → in_progress`); the conditions to start are met.
- `satellites-story-plan-review` — the plan the agent wrote into the story is sound (feeds the dynamic-planning `story-create` flow).
- `satellites-story-done-review` — the change satisfies the acceptance criteria.

A reader sees owner, object, moment, and action from the name alone,
and gates sort together by object.
