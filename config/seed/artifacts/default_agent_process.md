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
- **workflow is a list of contract names per story**
  (architecture.md §5). There is no separate workflow table —
  the ordered list of `contract_instance` rows on a story IS the
  workflow.
- **process order and evidence are first-class.** The
  `contract_claim` MCP handler is a server-side gate, not a
  convention. Predecessor CIs must be `passed` or `skipped` before
  a successor can claim. Evidence on the ledger is the trust
  leverage (`pr_0c11b762`).
- **session = one role.** `agent_role_claim` precedes
  `contract_claim`; sessions don't drift between hats. Reviewer is
  a separate runtime claiming review tasks, not a mode the
  orchestrator switches into.
- **five primitives per project** — projects, stories, contracts
  (instances + documents), documents, ledger.

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
   everything you need to choose the next call.
