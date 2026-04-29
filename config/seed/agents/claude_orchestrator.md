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
- Composes the per-story plan dynamically (story_66d4249f, S6 of
  `epic:orchestrator-driven-configuration`) — see the role spec
  below.

## Role spec — inputs and outputs

The orchestrator's plan-composition path runs in the Claude session
(per principle pr_f81f60ca: "satellites-agent is the worker;
orchestration lives elsewhere") and is implemented by the
`satellites_orchestrator_compose_plan` MCP verb.

### Inputs

| Input | Substrate origin |
|---|---|
| Story description + acceptance criteria | `satellites_story_get(id)` |
| User prompt / runtime intent | The current Claude session message stream (the `implement story_xxx` request and any clarifications) |
| Scope mandate stack | `type=workflow` documents at scope=system / scope=workspace / scope=project / scope=user, resolved via `loadResolvedWorkflowSpec` (story_f0a78759) |
| Active principles | `satellites_principle_list(active_only=true, project_id=...)` |
| Contracts catalog | `type=contract` documents at scope=system + scope=project, listed via `satellites_contract_list` |
| Agents catalog | `type=agent` documents at scope=system + scope=project, listed via `satellites_internal_agent_list` |
| Skills catalog | `type=skill` documents (currently surfaced via the `skills` field on contract responses) |

### Outputs

| Output | Substrate target |
|---|---|
| Per-story plan as ordered tasks | The `task` queue (per principle pr_75826278). Each task carries `{contract_name, agent_ref, sequence}` in its Payload (origin=story_stage). |
| `kind:plan` ledger row | One ledger row scoped to the story, written **before** the workflow-claim row. Structured payload mirrors the `proposed_contracts` list and the agent assignments. |
| Workflow claim | `satellites_story_workflow_claim(story_id, proposed_contracts=[...], claim_markdown=...)` — emits the `ContractInstance` rows + the `kind:workflow-claim` ledger row. |

### Constraints

- The orchestrator MUST include every contract in the resolved scope
  mandate stack. The reviewer enforces the floor at workflow claim
  time via `mandatory_slot_missing` (story_f0a78759 surfaces the
  source layer in the JSON error body).
- Agent picking: the default rule matches a system agent whose name
  equals `<contract_name>_agent` or `agent_<contract_name>`; callers
  may override per-slot via the `agent_overrides` argument. After the
  S8 audit collapses the 1-1 contract shadows into role agents, this
  rule is replaced by capability-based selection.

## How

The agent is a system-scope document referenced by the session
registry. It is not "executed" the way lifecycle agents are — its
purpose is to anchor the role-grant the session inherits and to
codify the plan-composition contract above.

## Limitations

- One agent per session. The SessionStart path mints exactly one
  orchestrator grant per registered chat UUID.
- Configuration changes (e.g. tightening `tool_ceiling`) require a
  re-seed; existing sessions keep the grant they minted at start.
