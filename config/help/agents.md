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

- `developer_agent` — drives plan (readiness assessment + design +
  task decomposition) and develop (writes code and commits).
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

## Session role-claim flow (epic:v4-lifecycle-refactor)

Sessions are role-bound. A Claude Code session inherits an
orchestrator grant at SessionStart; before it can claim a contract
the session's grant must already be active. `contract_claim` is
gated by `resolveRequiredRoleGrant`:

1. The contract document carries `required_role` (a role doc id).
2. The session row carries `OrchestratorGrantID` (set at
   SessionStart, cleared by `agent_role_release`).
3. The grant carries `RoleID` (the role doc the session is acting as).
4. `contract_claim` rejects with `grant_required` when the session
   has no active grant, and with `required_role_mismatch` when the
   grant's role doesn't match the contract's required role.

The release path: a session calls `agent_role_release(grant_id)` to
end its claim window; subsequent `contract_claim` calls from that
session return `grant_required` ("not active") until a fresh grant
is minted on a new session.
