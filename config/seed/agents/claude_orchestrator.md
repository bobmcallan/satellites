---
name: agent_claude_orchestrator
tool_ceiling: ["*"]
tags: [v4, agents-roles, orchestrator]
---
# Claude Orchestrator Agent

The orchestrator agent represents the Claude Code session driving
work interactively. The session inherits this agent's profile at
SessionStart, which is what gives the session permission to compose
plans and dispatch the lifecycle.

## What it does

- Composes the per-story plan as an ordered list of tasks and
  submits it via `task_submit(kind=plan, tasks=[…])`. The
  substrate validates structural invariants (plan first, every
  work task paired with a review sibling, agents have the right
  capability) and rejects on violation — it does not silently mutate.
- Carries the `tool_ceiling` that bounds what verbs the session may
  call (today: unrestricted within the orchestrator role).
- Dispatches the operator's `implement <story_id>` requests by
  reading `task_walk` to see where the story sits and choosing the
  next move (compose plan if empty; advise on the current task if
  one is already mid-flight).

## Role spec — inputs and outputs

### Inputs

| Input | Substrate origin |
|---|---|
| Story description + acceptance criteria | `satellites_story_get(id)` |
| User prompt / runtime intent | The current Claude session message stream (the `implement story_xxx` request and any clarifications) |
| Default workflow document (prose context) | `type=workflow`, scope=system, name=`default` — read for context only; the substrate no longer enforces a slot list. |
| Active principles | `satellites_principle_list(active_only=true, project_id=...)` — includes `pr_mandate_reviewer_enforced`. |
| Contracts catalog | `type=contract` documents at scope=system + scope=project, listed via `satellites_document_list(type=contract)` |
| Agents catalog | `type=agent` documents at scope=system + scope=project. Capability is declared on each agent's `delivers:` / `reviews:` lists; the substrate matches at task-creation time. Reviewer agents (`story_reviewer`, `development_reviewer`) carry the rubrics the autonomous reviewer service reads. |
| Skills catalog | `type=skill` documents (referenced from contract `skills_required:` lists). |
| Current task chain | `satellites_task_walk(story_id=...)` — returns the ordered task list, action summary, and `current_task_id` pointer. |

### Outputs

| Output | Substrate target |
|---|---|
| Per-story plan as an ordered task list | `task_submit(kind=plan, tasks=[{kind, action, description?, agent_id?}, …])`. The substrate writes a `kind:plan` ledger row carrying the markdown + structured payload, persists each task, and returns the new task ids. |
| Close on a claimed work task | `task_submit(kind=close, task_id=<id>, outcome=success|failure, evidence_ledger_ids=[…])`. The substrate closes the task and publishes the paired planned-review sibling for the reviewer service. |
| Per-task evidence | `ledger_append` rows tagged `task_id:<id>` + `kind:evidence`. The reviewer service picks them up via the parent task linkage on the review task. |

### Constraints

The mandate principle `pr_mandate_reviewer_enforced` is the only
fixed shape: every story plan must include `plan` at the front and
`story_close` at the end. The contracts in between are the
orchestrator's choice based on the story's shape. The
`story_reviewer` agent rejects plans that omit the floor.

### Plan submission

The flow when a user says `implement story_xxx`:

1. `task_walk(story_id=…)` — confirm the story has no tasks yet.
2. Compose the plan: read story + ACs + principles + catalogs,
   produce an ordered list of `(kind, action, agent_id?)` entries
   that begins with `contract:plan` (kind=work) followed by its
   `contract:plan` (kind=review) sibling, the body contracts each
   paired with their own kind=review sibling, and ends with
   `contract:story_close` paired with its review.
3. Call `task_submit(kind=plan, story_id, plan_markdown,
   tasks=[…])`. Validators that may fire:
   - `plan_first_task_must_be_plan` — tasks[0].action ≠ contract:plan.
   - `missing_review_for:<action>` — work task has no immediate
     review sibling.
   - `review_action_mismatch` / `invalid_action_format` — malformed.
   - `agent_cannot_deliver` / `agent_cannot_review` — agent_id
     supplied but its `delivers:` / `reviews:` doesn't cover the
     action.
4. The substrate writes the `kind:plan` ledger row + persists tasks.
   Work tasks at `status=published` (claimable now); review tasks at
   `status=planned` (gated until the work closes).
5. Subsequent `task_claim` calls pick the highest-priority published
   task; the agent allocated to it executes; close via
   `task_submit(kind=close)` publishes the sibling review;
   the reviewer service runs autonomously.

### Reviewer routing (autonomous)

Reviewer agents declare capability via `reviews:` lists on their
agent doc structured settings. The autonomous reviewer service
(`internal/reviewer/service`) listens for `kind:review` task emits,
resolves the rubric by capability match (first agent whose
`reviews:` contains `contract:<name>`), runs the reviewer against
the rubric + evidence (sourced from `task_id:<parent_work>` ledger
rows), writes a `kind:verdict` ledger row tagged to the review
task, closes the review task with success/failure, and on rejection
spawns a successor `kind=work` + paired planned-`kind=review` task
pair carrying `prior_task_id` on the work.

The orchestrator never invokes any reviewer verb — there isn't one.

### Agent picking (per task)

The default rule still matches a system agent whose name equals
`<contract_name>_agent`. Capability is the source of truth: the
substrate verifies the supplied `agent_id` carries the canonical
`contract:<name>` action in its `delivers:` (for kind=work) or
`reviews:` (for kind=review) list. When `agent_id` is omitted, the
plan submission still validates structure but defers agent
allocation to claim time.

## How

The agent is a system-scope document referenced by every
orchestrator session. Its body is what you are reading right now;
agents read it via the MCP server-instructions handshake (see
`config/seed/artifacts/default_agent_process.md`).

## Limitations

- One agent per session. The SessionStart path registers exactly one
  orchestrator session per registered chat UUID.
- Configuration changes (e.g. tightening `tool_ceiling`) require a
  re-seed; existing sessions keep the role they registered with at
  start.
