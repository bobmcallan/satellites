---
title: Workflows
slug: workflows
order: 40
tags: [help, workflows]
---
# Workflows

A **workflow** declares which contracts a story passes through,
in what order, and how many instances of each are permitted. The
shape is recorded as a `type=workflow` document.

## Default system workflow

Five required slots:

`plan → develop → push → merge_to_main → story_close`

- `develop` permits 1–10 instances; the others are exactly one
  each.
- Between required slots, optional middle contracts can be
  inserted when the project's workflow_spec admits them.

## Where workflows are configured

System defaults live at `config/seed/workflows/default.md`.
Project-scope overrides live as `type=workflow` documents
created via MCP and visible on the project's Configuration page.

## Limitations

- Slot constraints are enforced by the reviewer (`story_reviewer`)
  during plan submission; proposed contract lists that miss the
  mandated front-floor (`plan`) or end-floor (`story_close`) are
  rejected with `needs_more`.
- Reordering slots is not supported. A workflow's order is its
  contract — changing it mid-flight breaks downstream evidence
  expectations.
