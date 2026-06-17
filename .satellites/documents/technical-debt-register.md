---
name: technical-debt-register
type: document
tags: [tech-debt-register, content-review:allow-refs]
---

# Reviewer quarantine register

A quarantine register of **known, owned** failing checks — the data a reviewer
gate reconciles its functional-check failures against. This is a generic
[[reviewer-quarantine]] capability; `technical-debt-review` (sty_dd128ef6) is its
current consumer. The doc key stays `technical-debt-register` for reference
stability (the gate reads it by name); the capability it serves is reviewer-wide,
not technical-debt-specific.

The rule lives once in [[reviewer-quarantine]] and is summarised here: a gate
lets a failure pass only when this register names it **and** names the story that
owns it. A failing check that is not here is a *new red* and blocks the commit; a
row with no `story_id` is *unowned* and also blocks (you may not pad the register
to dodge the gate).

The register **only shrinks**: when a quarantined check goes green, its row is
removed (the owning story closes the window). It grows only as a deliberate,
story-backed capture of a failure that cannot be fixed in the moment.

| check_id | story_id | reason |
| --- | --- | --- |

_The register is currently empty — no quarantined checks. `TestStoryPanel_FilterBugs` was hardened (deterministic readiness polls replacing fixed settle sleeps) and its row removed by sty_b8aac474._
