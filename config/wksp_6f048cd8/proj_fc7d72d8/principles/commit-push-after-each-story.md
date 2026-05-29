---
name: commit-push-after-each-story
tags: [principles:project]
---
# Run /commit-push after each story

End each story with `/commit-push`. The routine bumps `.version`,
pushes the release tag, and triggers test → release → deploy. Until
that chain runs, the change is invisible to reviewers, other agents,
and the build pipeline.

## Why

Reviewers may judge against the latest pushed commit, not your local
working tree. Skipping the push makes the reviewer judge stale code —
the most common cause of false rejections.

## When to apply

- At every story completion, before requesting review.
- At natural mid-story checkpoints (end of a phase, end of a
  meaningful change).
- Before any operation that depends on the deploy chain having
  completed.

Do not batch multiple stories into one push — the per-story commit is
the unit of evidence each reviewer reads.
