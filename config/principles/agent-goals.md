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

**The workflow is the authority.** Follow every transition it declares — the
entry gate, the checkpoint, and the commit-push / ship / deploy step that closes
it — without pausing to ask permission for a step the workflow itself prescribes.
A step the workflow declares is authorised BY the workflow; it is never a
"block", even when it pushes, deploys, or mutates shared state. A block is only a
gap that PREVENTS following the workflow — the cases above — not a normal step on
its path.

See [[reviewer-only-model]].
