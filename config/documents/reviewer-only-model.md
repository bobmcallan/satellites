---
name: reviewer-only-model
scope: system
tags: [principles:global]
---
# The reviewer-only execution model

satellites runs one model, and every client obeys it. Roles are fixed; the
process is configuration; story status decides what is valid now.

## Roles

- **The agent is the executor.** The local agent does the work. Its one goal
  is to drive a story to `done` — read the story, do what the current state
  calls for, request review at each gated transition, iterate on rejection.
- **satellites is the reviewer, only.** A reviewer is a fresh-context gate on
  a transition: it judges whether the story may advance and **enacts** the
  status change under a reviewer-role key. It does not write the code, fix the
  story, or execute work.

The split is structural, not etiquette: the api-key role gate refuses a status
transition from an executor key. The agent cannot self-advance; the reviewer
does not do the work. Neither role can take the other's move.

## Process is skills

The states a story moves through, their order, and which transitions a reviewer
gates are **skills**, configured per project — not branches in the binary.
Change the skill, change the process; no release.

## Status gates what is valid

A story's **status** decides which skill and which transition apply right now.
The agent does not choose freely: it matches the story's type and status to the
one skill that applies, and follows it. A transition fires only when the
reviewer named on it accepts; `done` is the terminal status reached with every
gate on the path accepted — nothing else is done.

See [[process-as-configuration]], [[reviewer-process]], [[agent-goals]].
