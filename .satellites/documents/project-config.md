# project-config (satellites)
#
# Residual project-scoped settings that have no natural skill home.
# Body is YAML inside a markdown fence so humans can annotate outside it.
#
# NOTE: workflow dispatch is NOT here. Which workflow a story type uses is
# resolved by `applies_to ↔ category`: the governing-workflow resolver scans the
# client-dir workflow files under `.satellites/workflows/*.md` FIRST, then the
# materialised `kind:workflow` skills, and picks the one whose `applies_to`
# covers the category (exact match beats the `*` wildcard). A workflow is
# repo-owned CONFIG (a markdown file: frontmatter `name` + `applies_to`, a fenced
# ```yaml state machine) — `satellites workflow embed <story>` stamps the
# resolved copy into the story. The `story_types` mapping that used to live here
# is retired; `applies_to` is the single source. The gate's dispatch path never
# reads this document.

```yaml
# Per-transition step summariser (sty_2517f6b8). After each gated transition
# `satellites story review` runs this skill and records its prose as a
# step_summary ledger row, surfaced on the portal /ledger page. This is a
# post-transition setting, not dispatch. Empty/absent disables summaries.
# Names a skill resolved at .claude/skills/<name>/SKILL.md.
step_summariser_skill: satellites-story-summary
```

## Notes

A `story_type` is bound to its workflow by the workflow's `applies_to`, not by an
entry here — add or rebind a story type by authoring (or extending the
`applies_to` of) a workflow file under `.satellites/workflows/`, or a
`kind:workflow` skill. A client-dir workflow wins over a same-`applies_to`
materialised skill.

This document holds only settings the dynamic index cannot express — currently
just the optional `step_summariser_skill`.
