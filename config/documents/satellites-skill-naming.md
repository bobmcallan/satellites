---
name: satellites-skill-naming
type: document
scope: system
tags: [principles:global]
---

# Satellites naming encodes ownership

Every substrate-owned artifact carries a structured `satellites-` name, and the
prefix is the one required marker: it declares the substrate owns the artifact,
distinguishing it from operator-authored ones. An owned artifact that reads as
unprefixed locally is a defect, not an exception. What follows the prefix is
structured by the artifact's kind so that owner, kind, and role read from the
name alone:

- Materialised skills in `.claude/skills/` are `satellites-<name>`; workflow
  skills are `satellites-<type>-workflow`.
- Reviewer gates are `satellites-<object>-<stage>-review` — `object` is what is
  gated (`story`, `doc`, …), `stage` is the transition guarded (`start`,
  `plan`, `done`, …).

The source file under `.satellites/skills/` need not carry the prefix; sync
ensures it on materialisation and reconciles by the injected stamp
(`document_id`), never by name.
