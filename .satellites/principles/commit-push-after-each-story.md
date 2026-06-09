---
name: commit-push-after-each-story
tags: [principles:project]
---
# Run the commit-push checkpoint after each story

End each story by running the `satellites-commit-push` skill — the
process-owned checkpoint that makes the change visible to reviewers, other
agents, and the build pipeline. Run it at every story completion before
requesting review, and at natural mid-story checkpoints.

Do not batch multiple stories into one push: the per-story commit is the unit of
evidence each reviewer reads, and a reviewer may judge against the latest pushed
commit, not your local tree.
