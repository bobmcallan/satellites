---
name: satellites-skill-naming
type: document
tags: [principles:project]
---

# Satellites skill naming

## Two identities: the stamp (machine) and the `satellites-` prefix (human)

A materialised substrate skill carries two identities; both assert the same
fact — the substrate owns this skill — at two layers.

- **Machine identity — the injected stamp.** `satellites skill sync` reconciles
  the local `.claude/skills/` tree by each materialised skill's injected stamp
  (`document_id` / `version` / `hash` — see the skill-sync reference contract),
  NOT by name. sync only updates or removes a skill it materialised (one
  carrying the stamp); a skill with no stamp is operator-authored and is never
  touched. Because the key is `document_id`, a rename or deletion in the
  substrate propagates correctly regardless of the local name.
- **Human identity — the `satellites-` prefix.** Every materialised substrate
  skill in `.claude/skills/` is named `satellites-<name>`. The prefix is the
  at-a-glance marker that the substrate owns a skill, telling it apart from an
  operator-authored skill such as `commit-push`. It is **required on the local
  name**: a substrate skill that reads as unprefixed locally is a defect, not a
  permitted exception — workflow skills are named `satellites-<type>-workflow`
  like every other substrate skill.

The **source** name (`.satellites/skills/<name>.md`) need not carry the
prefix — the local prefix is ensured when the skill is materialised. The stamp
keys the reconcile; the prefix marks ownership for a human reader.

## Reviewer gates: `satellites-<object>-<stage>-review`

- **object** — what is gated: `story`, `doc`, …
- **stage** — the transition guarded: `start`, `plan`, `done`, …

The story-lifecycle gates are therefore:

- `satellites-story-start-review` — start of work (`ready → in_progress`); the conditions to start are met.
- `satellites-story-plan-review` — the plan the agent wrote into the story is sound (feeds the dynamic-planning `story-create` flow).
- `satellites-story-done-review` — the change satisfies the acceptance criteria.

A reader sees owner, object, moment, and action from the name alone,
and gates sort together by object.
