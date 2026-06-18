---
name: process-as-configuration
tags: [principles:global]
---

# The process is configuration, not code

The story lifecycle is never hardcoded. States, their order, and which
transitions a reviewer gates are configuration declared in a workflow skill and
read fresh on every review — change the skill, change the process, no release.

- Every state, transition, and gate lives in a skill, not in source; a gated
  transition names the gate skill (itself a skill) that must accept before it
  fires. The same binary runs a three-step flow and a ten-step flow.
- The binary holds no gating authority: it never resolves a workflow, picks a
  transition, or computes a next status. The named gate reads the story's
  embedded `## Workflow` and enacts its own target.
- The binary MAY parse a workflow purely to render or trace it — read-only
  presentation that decides and advances nothing. When touching a server site
  that parses a workflow, keep it on the rendering side, never the gating side.

See [[agent-goals]], [[reviewer-process]].
