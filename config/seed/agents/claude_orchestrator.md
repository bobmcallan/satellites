---
name: agent_claude_orchestrator
permitted_roles: ["role_orchestrator"]
tool_ceiling: ["*"]
tags: [v4, agents-roles, orchestrator]
---
# Claude Orchestrator Agent

The orchestrator agent represents the Claude Code session driving
work interactively. The session inherits this agent's role-grant at
SessionStart, which is what gives the session permission to claim
contracts and coordinate the lifecycle.

## What it does

- Provides the role-grant the session_register path mints when the
  SessionStart hook registers a fresh chat UUID.
- Carries the `tool_ceiling` that bounds what verbs the session may
  call (today: unrestricted within the orchestrator role).

## How

The agent is a system-scope document referenced by the session
registry. It is not "executed" the way lifecycle agents are — its
purpose is to anchor the role-grant the session inherits.

## Limitations

- One agent per session. The SessionStart path mints exactly one
  orchestrator grant per registered chat UUID.
- Configuration changes (e.g. tightening `tool_ceiling`) require a
  re-seed; existing sessions keep the grant they minted at start.
