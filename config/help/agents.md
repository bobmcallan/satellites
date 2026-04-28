---
title: Agents
slug: agents
order: 20
tags: [help, agents]
---
# Agents

An **agent** is a configured executor — a bundle of permission
patterns, role grants, and skill references that contracts allocate
when work needs doing.

## System agents

Seeded from `config/seed/agents/*.md`, system agents power the
lifecycle:

- `preplan_agent` — read-only investigation.
- `plan_agent` — designs the implementation strategy.
- `develop_agent` — writes code and commits.
- `push_agent` — pushes to origin.
- `merge_agent` — fast-forwards local main.
- `story_close_agent` — transitions stories to terminal state.
- `agent_claude_orchestrator` — anchors the role-grant inherited
  by interactive Claude sessions.

## How agents are configured

Each agent's markdown frontmatter declares its
`permission_patterns` (the tool-call patterns the enforce hook
admits when the agent is claimed) and any role/skill references.
The body is the human description: what it does, how, and what it
explicitly cannot do.

## Limitations

- Agents do not choose their next task. Orchestration lives in the
  Claude session (interactive) or scheduled tasks (server-side).
- Re-seeding (`/admin/system-config`) updates the document body and
  structured payload but does not interrupt running claims; in-flight
  contract instances keep the patterns they already minted.
