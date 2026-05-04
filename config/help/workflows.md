---
title: Workflows
slug: workflows
order: 40
tags: [help, workflows]
---
# Workflows

A **workflow** is a default shape the orchestrator references when
composing per-story plans. The shape is recorded as a
`type=workflow` document; the substrate does not enforce it
directly. The orchestrator submits a per-story task list via
`story_task_submit(kind=plan)` and the reviewer judges whether the
shape is appropriate.

## Default system workflow

`plan → develop → push → merge_to_main → story_close`

Each contract surfaces as a paired (kind=work, kind=review) task
in the submitted plan. Multiple `develop` pairs are permitted when
a story splits naturally (e.g. backend then frontend).

## Where workflows are configured

System defaults live at `config/seed/workflows/default.md`.
Project-scope overrides live as `type=workflow` documents created
via MCP and visible on the project's Configuration page.

## Limitations

- Workflow shape is enforced by the reviewer
  (`story_reviewer`) at plan-review time; proposed task lists that
  miss the mandated front-floor (`contract:plan`) or end-floor
  (`contract:story_close`) are rejected, which spawns a successor
  task pair so the orchestrator can resubmit.
- Reordering tasks is not supported mid-flight. The plan submitted
  via `story_task_submit(kind=plan)` is committed; the orchestrator
  iterates by spawning successor task pairs (substrate-driven on
  rejection) rather than rewriting the chain.
