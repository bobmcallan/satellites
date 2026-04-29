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
- Composes the per-story plan dynamically and runs the plan-approval
  loop with the reviewer (`epic:configuration-over-code-mandate`,
  story_a5826137) before any contract is claimed.
- Dispatches each contract close to the appropriate reviewer agent
  (story_b4d1107c).

## Role spec — inputs and outputs

The orchestrator's plan-composition path runs in the Claude session
(per principle pr_f81f60ca: "satellites-agent is the worker;
orchestration lives elsewhere") and is implemented by the
`satellites_orchestrator_compose_plan` MCP verb (legacy single-shot)
and the `satellites_orchestrator_submit_plan` verb (loop-style
plan approval introduced by `epic:configuration-over-code-mandate`).

### Inputs

| Input | Substrate origin |
|---|---|
| Story description + acceptance criteria | `satellites_story_get(id)` |
| User prompt / runtime intent | The current Claude session message stream (the `implement story_xxx` request and any clarifications) |
| Default workflow document (prose context) | `type=workflow`, scope=system, name=`default` — read for context only; the substrate no longer enforces a slot list (story_af79cf95). |
| Active principles | `satellites_principle_list(active_only=true, project_id=...)` — includes `pr_mandate_reviewer_enforced` (story_e0833aea). |
| Contracts catalog | `type=contract` documents at scope=system + scope=project, listed via `satellites_contract_list` |
| Agents catalog | `type=agent` documents at scope=system + scope=project, listed via `satellites_internal_agent_list`. Includes the reviewer agents `story_reviewer` and `development_reviewer`. |
| Skills catalog | `type=skill` documents (currently surfaced via the `skills` field on contract responses) |

### Outputs

| Output | Substrate target |
|---|---|
| Per-story plan as ordered tasks | The `task` queue (per principle pr_75826278). Each task carries `{contract_name, agent_ref, sequence}` in its Payload (origin=story_stage). |
| `kind:plan` ledger row | One ledger row scoped to the story, written **before** plan submission. Structured payload mirrors the `proposed_contracts` list and the agent assignments. |
| `kind:plan-approved` ledger row | Written by `satellites_orchestrator_submit_plan` when the reviewer accepts. The row's existence is the precondition for `workflow_claim`. |
| Workflow claim | `satellites_story_workflow_claim(story_id, proposed_contracts=[...], claim_markdown=...)` — emits the `ContractInstance` rows + the `kind:workflow-claim` ledger row. Rejected with `plan_not_approved` when no plan-approved row exists. |

### Constraints

The mandate principle `pr_mandate_reviewer_enforced` (story_e0833aea) is
the only fixed shape: every story must include `preplan` + `plan` at
the front and `story_close` at the end. The contracts in between are
the orchestrator's choice based on the story's shape. The
`story_reviewer` agent rejects plans that omit the floor; the
substrate does not enforce it (story_af79cf95 removed the slot
algebra).

### Plan-approval loop

The flow when a user says `implement story_xxx`:

1. Compose a plan: read story + ACs + principles + catalogs, produce
   an ordered list of `(contract_name, agent_ref)` pairs that begins
   with `preplan + plan` and ends with `story_close`.
2. Write the `kind:plan` ledger row.
3. Call `satellites_orchestrator_submit_plan(story_id, plan_markdown,
   proposed_contracts, iteration=1)`.
4. On `verdict=accepted`: proceed to `workflow_claim`. The verb has
   already written the `kind:plan-approved` row.
5. On `verdict=needs_more`: read `review_questions[]`, revise the
   plan, increment `iteration`, resubmit. Repeat.
6. On `error=plan_review_iteration_cap_exceeded`: stop and surface
   the failure to the user; the iteration cap is KV-configurable
   (`plan_review_max_iterations`, default 5).

### Reviewer mapping (per-contract close)

When a contract close fires (`satellites_story_contract_close`), the
substrate dispatches the reviewer based on contract name
(story_b4d1107c, `runReviewer` in `internal/mcpserver/close_handlers.go`):

- `develop` → `development_reviewer.Body` is the rubric.
- everything else (`preplan`, `plan`, `push`, `merge_to_main`,
  `story_close`, and any project-scope contract) → `story_reviewer.Body`.

The reviewer (`reviewer.Reviewer`, Gemini-backed in production —
story_b4d1107c) reads the rubric + evidence and returns
`accepted` / `rejected` / `needs_more`. On `needs_more` the
orchestrator reads the review questions, addresses them via
`satellites_story_contract_respond`, and re-closes.

### Agent picking (proposed contracts)

The default rule still matches a system agent whose name equals
`<contract_name>_agent` or whose role-shaped agent body covers the
contract; callers may override per-slot via the `agent_overrides`
argument on `orchestrator_compose_plan`. Reviewer agents
(`story_reviewer`, `development_reviewer`) are not assigned to
proposed slots — the substrate dispatches them directly at close
time.

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
