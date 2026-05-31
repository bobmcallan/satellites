---
name: satellites-skill-naming
type: document
tags: [principles:project]
---

# Satellites skill naming

## Ownership is the identity stamp; the `satellites-` prefix is advisory

`satellites skill sync` reconciles the local `.claude/skills/` tree against
the substrate by each materialised skill's **injected identity stamp**
(`document_id` / `version` / `hash` — see the skill-sync reference contract),
NOT by name. sync only updates or removes a skill it materialised (one
carrying the stamp); a skill with no stamp is operator-authored and is never
touched. Because the stamp keys on `document_id`, a rename or deletion in the
substrate propagates correctly even for skills that carry no name prefix
(e.g. the `feature-workflow` / `fix-workflow` workflow skills).

The `satellites-` prefix is retained as a **human-readable owner marker** —
it tells a reader at a glance that the substrate owns a skill — but it is no
longer the reconcile key. Most substrate-owned skills carry it; workflow
skills are the deliberate exception (their name is the story-type they apply
to).

## Reviewer gates: `satellites-<object>-<stage>-review`

- **object** — what is gated: `story`, `doc`, …
- **stage** — the transition guarded: `start`, `plan`, `done`, …

The story-lifecycle gates are therefore:

- `satellites-story-start-review` — start of work (`ready → in_progress`); the conditions to start are met.
- `satellites-story-plan-review` — the plan the agent wrote into the story is sound (feeds the dynamic-planning `story-create` flow).
- `satellites-story-done-review` — the change satisfies the acceptance criteria.

A reader sees owner, object, moment, and action from the name alone,
and gates sort together by object.
