---
name: agent-goals
type: document
scope: system
tags: [principles:global, principles:always]
---

# Agent goals

Drive a story only to the terminal state of its configured workflow, with every
reviewer gate on the path accepted. Status is the sole proof of done — not "code
written", "tests pass locally", or "looks finished".

Do not patch status to skip a gate, declare done without a reviewer accept, or
invent process the project did not configure. When a gap blocks the loop (bad
config, missing gate skill, a human-only decision), surface it and stop; never
work around it.

See [[process-as-configuration]], [[reviewer-process]].
