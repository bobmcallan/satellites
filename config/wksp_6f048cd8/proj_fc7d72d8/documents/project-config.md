# project-config (satellites)
#
# Project-scoped configuration for the satellites repo itself.
# Read by workflow skills, reviewers, and the loop verb on every
# story_request_review call. Body is YAML inside a markdown fence
# so humans can also annotate freely outside the block.
#
# Format documented in docs/document-types-v5.md.

```yaml
story_types:
  # Default — most stories. Plan-reviewed, completion-reviewed.
  feature:
    workflow_skill: .claude/skills/feature-workflow.md

  # Smaller fixes — plan-review on entry, done-review on completion.
  fix:
    workflow_skill: .claude/skills/fix-workflow.md

  # Refactors, bug-fixes and infrastructure changes ride the same two-gate
  # fix workflow: plan-review on backlog → in_progress, done-review on
  # in_progress → done. It is the loop-proven path and gives both a plan
  # gate and a completion gate, which every change category warrants.
  refactor:
    workflow_skill: .claude/skills/fix-workflow.md
  bug:
    workflow_skill: .claude/skills/fix-workflow.md
  infrastructure:
    workflow_skill: .claude/skills/fix-workflow.md

# Per-project reviewer overrides. Rare. Most reviewers are declared by
# the workflow skill itself; this block only exists for cases where a
# specific story_type wants a different gate than the workflow's default.
reviewer_overrides: {}
```

## Notes

Every story category the epic uses (feature, fix, refactor, bug,
infrastructure) maps to a workflow skill. The loop verb fails loud (not
fall back) on an unknown story_type, so adding a category here is the way
to let the loop drive it.

When adding a new `story_type`, also add a corresponding workflow skill at
`.claude/skills/<name>-workflow.md` (sourced from
`config/<wksp>/<proj>/skills/` and materialised by satellites-init).
