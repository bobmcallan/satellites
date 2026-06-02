# project-config (satellites)
#
# Residual project-scoped settings that have no natural skill home.
# Body is YAML inside a markdown fence so humans can annotate outside it.
#
# NOTE (sty_815c09e7): workflow dispatch is NOT here. Which workflow a story
# type uses is the dynamic skill index — the kind:workflow skill whose
# `applies_to` contains the story type (`satellites skill index`). The
# `story_types` mapping that used to live here is retired; `applies_to` is the
# single source. The gate's dispatch path never reads this document.

```yaml
# Per-transition step summariser (sty_2517f6b8). After each gated transition
# `satellites story review` runs this skill and records its prose as a
# step_summary ledger row, surfaced on the portal /ledger page. This is a
# post-transition setting, not dispatch. Empty/absent disables summaries.
# Names a skill resolved at .claude/skills/<name>/SKILL.md.
step_summariser_skill: satellites-story-summary
```

## Notes

A `story_type` is bound to its workflow by the workflow skill's `applies_to`
(see `satellites skill index`), not by an entry here — add a story type by
authoring (or extending the `applies_to` of) a `kind:workflow` skill.

This document holds only settings the dynamic index cannot express — currently
just the optional `step_summariser_skill`.
