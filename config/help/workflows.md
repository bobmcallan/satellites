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

Six required slots:

`preplan → plan → develop → push → merge_to_main → story_close`

- `develop` permits 1–10 instances; the others are exactly one
  each.
- Between required slots, optional middle contracts can be
  inserted when the project's workflow_spec admits them.

## Where workflows are configured

System defaults live at `config/seed/workflows/default.md`.
Project-scope overrides live as `type=workflow` documents
created via MCP and visible on the project's Configuration page.

## Limitations

- Slot count constraints are enforced at `workflow_claim` time;
  proposed contract lists that miss a required slot are rejected
  with `missing_required_slot`.
- Reordering slots is not supported. A workflow's order is its
  contract — changing it mid-flight breaks downstream evidence
  expectations.
