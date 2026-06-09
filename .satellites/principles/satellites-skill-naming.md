---
name: satellites-skill-naming
type: document
tags: [principles:project]
---

# Satellites skill naming

Every materialised substrate skill in `.claude/skills/` is named
`satellites-<name>` — the prefix is the required marker that the substrate owns
the skill, distinguishing it from operator-authored skills. A substrate skill
that reads as unprefixed locally is a defect, not an exception (workflow skills
are `satellites-<type>-workflow` like any other). The source file under
`.satellites/skills/` need not carry the prefix; sync ensures it on
materialisation and reconciles by the injected stamp (`document_id`), never by
name.

Reviewer gates are named `satellites-<object>-<stage>-review` — `object` is what
is gated (`story`, `doc`, …), `stage` is the transition guarded (`start`,
`plan`, `done`, …) — so owner, object, and moment read from the name alone.
