---
name: default_agent_process
tags: [kind:agent-process, v4]
---
# satellites · agent process

This block is the satellites MCP server's instructions to your session.
It tells you the *fundamentals* of how this substrate works and the
two routing rules you must apply before any project-scoped work.

## fundamentals

- **configuration over code** — satellites' behaviour is data
  (contracts, agents, configurations, principles) not code paths.
  New behaviour is added by writing rows, not by branching code.
  See `docs/architecture-configuration-over-code-mandate.md`.
- **story is the unit of work** (`pr_a9ccecfb`). Every change you
  make ties to a story id. There is no work outside a story.
- **story = task chain.** A story's tasks (rows where
  `story_id=<id>`, ordered by created_at) are the conversation log
  AND the workflow. There is no separate workflow / contract_instance
  table — the ordered task list IS the workflow.
- **plan as agent-authored task list.** The orchestrator submits
  the full plan via `task_submit(kind=plan, tasks=[…])`.
  Substrate validates structural invariants (plan first, every work
  task has a paired review sibling, actions well-formed, agents have
  the right capability) and rejects on violation — it does not
  silently mutate.
- **tasks are thin; ledger rows are the artifacts.** A task carries
  only what's needed to dispatch + order the work (id, story_id,
  kind, action, agent_id, parent_task_id, prior_task_id, status,
  description). Plan markdown, evidence, and verdicts live as
  ledger rows linked to the task by `task_id:<id>` tags.
- **agent capability via frontmatter.** Agents declare what they
  can do via `delivers:` / `reviews:` lists in their document
  structured settings. The substrate matches at task-creation time
  (`task_submit` rejects `agent_cannot_deliver` /
  `agent_cannot_review` mismatches).
- **session = one agent.** Sessions don't drift between hats.
  The reviewer service is a separate in-process runtime that
  subscribes to `kind:review` task emits, runs the rubric, writes
  the verdict ledger row, closes the task, and on rejection spawns
  a successor `kind=work` + paired planned-`kind=review` pair with
  `prior_task_id` set on the work task.
- **process order is enforced server-side.** `task_claim` is a
  gate: agents claim what's published; review tasks stay at
  `status=planned` until their sibling work task closes.
- **five primitives per project** — projects, stories, tasks,
  documents (contracts/agents/principles/skills/workflows),
  ledger.

## routing rules

These rules are mandatory. Apply them in order.

1. **project context first.** Before any project-scoped MCP call,
   identify the active project. If a `project_id` is not pinned to
   your session, call `satellites_project_set(repo_url=…)`.
   Obtain the URL with `git remote get-url origin` if needed.
   The verb resolves the existing project for that remote or
   returns `no_project_for_remote` — in that case, ask the user
   whether to create the project explicitly via `project_create`.

2. **story routing.** When the operator says `implement <story_id>`
   (or `run <story_id>`), your first MCP call is
   `satellites_story_get(id=<story_id>)`. The result names the
   project, status, category, tags, and template-required fields —
   everything you need to choose the next call. Then call
   `satellites_task_walk(story_id=<id>)` to see the current task
   chain (with `current_task_id` pointing at the first non-terminal
   task) and pick the next move:

   - if no tasks exist, you are the orchestrator — compose the plan
     and submit via `task_submit(kind=plan, tasks=[…])`.
   - if a claimable task exists for your capability, claim it via
     `task_claim` and execute. When done, write any
     evidence/artifact ledger rows (tagged `task_id:<your_task>`)
     and call `task_submit(kind=close, task_id=<id>,
     outcome=success|failure, evidence_ledger_ids=[…])`.
   - the close path automatically publishes the paired review task;
     the reviewer service picks it up. You do not call any
     reviewer verb — there isn't one.
