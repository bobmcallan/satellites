---
name: story-execution-process
tags: ["principles:project", "principles:always"]
---
# Story execution process

How the agent drives a story to `done`.

- **Run the `satellites-commit-push` checkpoint** at every natural checkpoint
  (end of phase, end of meaningful change, before requesting review). It is the
  single point where work becomes visible to other agents, reviewers, and the
  build pipeline; skipping it makes the reviewer judge stale code.
- **Do not stop unless blocked.** Read the workflow skill, do the work, edit the
  story body, request the reviewer gate; on reject, read the notes and iterate.
  You are blocked only when a rejection needs information you cannot infer (a
  product decision, credential, or human-only system), a required tool/verb/skill
  is missing or broken, the workflow or project config is malformed and you may
  not fix it, a destructive/shared-state action needs operator confirmation, or
  the story carries a `dogfood-after:<story-name>` tag. A reviewer reject on
  technical grounds you can address is NOT blocked.
- **When blocked, write a `## Blocked` section** into the story body (what you
  tried, the precise reason, what the operator must provide) and stop; status
  stays where it is.
- **After substrate changes, run `satellites skill sync`** to refresh the local
  skill files — until it runs the next gate executes against stale reviewers and
  workflow.

## `dogfood-after:<story-name>` — operator-refresh gate

The tag is a stop signal: a story drivable only after a named predecessor has
landed AND the operator has refreshed their local environment. If the predecessor
is not completed, treat as blocked on it. If it is completed, stop immediately
and ask the operator to refresh (restart Claude so the MCP session picks up new
verbs and load-context, then re-bootstrap the CLI) before starting a new session.
Do not proceed in the current session against stale substrate.
